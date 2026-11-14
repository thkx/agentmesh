package coding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/agent"
	"github.com/thkx/agentmesh/internal/workspace"
	"github.com/thkx/agentmesh/internal/workspace/sandbox/docker"
	"github.com/thkx/agentmesh/internal/workspace/worktree"
)

type Agent interface {
	Run(context.Context, dom.Task) (dom.Result, error)
}
type Service struct {
	repo           *Repository
	agent          Agent
	workspaceRoot  string
	sandbox        docker.Runner
	worktrees      *worktree.Manager
	mu             sync.Mutex
	workspaceLocks sync.Map // task ID -> *sync.Mutex
	cancels        map[string]context.CancelFunc
	maxConcurrent  chan struct{}
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	closed         bool
}

func NewService(repo *Repository, agent Agent, workspaceRoot, image string, network bool) (*Service, error) {
	if repo == nil || agent == nil {
		return nil, errors.New("coding repository and agent runtime are required")
	}
	abs, e := filepath.Abs(workspaceRoot)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(abs, 0700); e != nil {
		return nil, e
	}
	if strings.TrimSpace(image) == "" {
		image = "golang:1.27"
	}
	worktrees, err := worktree.New(abs)
	if err != nil {
		return nil, err
	}
	serviceCtx, serviceCancel := context.WithCancel(context.Background())
	return &Service{repo: repo, agent: agent, workspaceRoot: abs, sandbox: docker.Runner{Image: image, NetworkEnabled: network}, worktrees: worktrees, cancels: map[string]context.CancelFunc{}, maxConcurrent: make(chan struct{}, 2), ctx: serviceCtx, cancel: serviceCancel}, nil
}
func (s *Service) Create(ctx context.Context, in CreateRequest) (Task, error) {
	if e := in.Validate(); e != nil {
		return Task{}, e
	}
	repo, e := filepath.Abs(in.RepositoryPath)
	if e != nil {
		return Task{}, e
	}
	info, e := os.Stat(repo)
	if e != nil || !info.IsDir() {
		return Task{}, fmt.Errorf("repository path must be an existing directory")
	}
	if _, e = git(ctx, repo, "rev-parse", "--show-toplevel"); e != nil {
		return Task{}, fmt.Errorf("repository is not a valid git repository: %w", e)
	}
	in.RepositoryPath = repo
	return s.repo.Create(ctx, in)
}
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, e := cmd.CombinedOutput()
	if e != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), e, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
func (s *Service) Run(ctx context.Context, id string) (Task, error) {
	return s.run(ctx, id, true)
}

func (s *Service) run(ctx context.Context, id string, acquireSlot bool) (Task, error) {
	if acquireSlot {
		select {
		case s.maxConcurrent <- struct{}{}:
		case <-ctx.Done():
			return Task{}, ctx.Err()
		}
		defer func() { <-s.maxConcurrent }()
	}
	unlock := s.lockTask(id)
	defer unlock()
	t, e := s.repo.Get(ctx, id)
	if e != nil {
		return Task{}, e
	}
	if t.Status != StatusPending {
		return Task{}, ErrConflict
	}
	claimed, e := s.repo.Claim(ctx, id)
	if e != nil {
		return Task{}, e
	}
	if !claimed {
		return Task{}, ErrConflict
	}
	t.Status = StatusRunning
	t.StartedAt = time.Now().UTC()
	baseCtx, baseCancel := context.WithCancel(ctx)
	stopServiceCancel := context.AfterFunc(s.ctx, baseCancel)
	defer func() { stopServiceCancel(); baseCancel() }()
	timeout := time.Duration(t.TimeoutSeconds) * time.Second
	runCtx, cancel := context.WithTimeout(baseCtx, timeout)
	defer cancel()
	s.mu.Lock()
	s.cancels[id] = cancel
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.cancels, id); s.mu.Unlock() }()
	_ = s.repo.Event(ctx, id, "task.started", nil)
	fail := func(err error) (Task, error) {
		t.Status = StatusFailed
		if errors.Is(err, context.Canceled) {
			t.Status = StatusCanceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Status = StatusFailed
		}
		t.Error = err.Error()
		t.FinishedAt = time.Now().UTC()
		_ = s.repo.Update(context.Background(), t)
		_ = s.repo.Event(context.Background(), id, "task.failed", map[string]string{"error": t.Error})
		return t, err
	}
	if e = runCtx.Err(); e != nil {
		return fail(e)
	}
	branch := "agentmesh/" + t.ID
	workspaceID := "coding-" + t.ID
	workspacePath := filepath.Join(s.workspaceRoot, workspaceID)
	ws, worktreeErr := s.worktrees.CreateBranch(runCtx, t.RepositoryPath, workspaceID, branch)
	if worktreeErr != nil {
		return fail(worktreeErr)
	}
	workspacePath = ws.Path
	t.WorkspaceID = workspaceID
	t.WorkspacePath = workspacePath
	t.BranchName = branch
	if e = s.repo.Update(runCtx, t); e != nil {
		return fail(e)
	}
	_ = s.repo.Event(runCtx, id, "workspace.created", map[string]string{"path": workspacePath, "branch": branch})
	agentTask := dom.Task{ID: "coding-" + t.ID, ModelID: t.ModelID, WorkspaceID: workspaceID, Prompt: buildPrompt(t), MaxSteps: 80, MaxToolCalls: 200, TimeoutSeconds: t.TimeoutSeconds, ApprovedTools: []string{"file.write"}}
	step := Step{TaskID: id, Sequence: 1, Kind: "agent.execute", Status: "running", StartedAt: time.Now().UTC()}
	result, agentErr := s.agent.Run(runCtx, agentTask)
	step.FinishedAt = time.Now().UTC()
	if agentErr != nil {
		step.Status = "failed"
		step.Error = agentErr.Error()
		_ = s.repo.AddStep(context.Background(), step)
		return fail(fmt.Errorf("coding agent: %w", agentErr))
	}
	step.Status = "succeeded"
	step.Output = result.Content
	if e = s.repo.AddStep(runCtx, step); e != nil {
		return fail(fmt.Errorf("persist agent step: %w", e))
	}
	command := t.TestCommand
	if command.Program == "" {
		command = detectCommand(workspacePath)
	}
	if command.Program == "" {
		_ = s.repo.Event(runCtx, id, "test.skipped", map[string]string{"reason": "no supported project manifest or explicit test command"})
	}
	if command.Program != "" {
		for attempt := 0; attempt <= t.MaxRepairAttempts; attempt++ {
			t.Status = StatusTesting
			t.RepairAttempts = attempt
			if e = s.repo.Update(runCtx, t); e != nil {
				return fail(e)
			}
			_ = s.repo.Event(runCtx, id, "test.started", map[string]any{"attempt": attempt + 1, "program": command.Program, "args": command.Args})
			testStep := Step{TaskID: id, Sequence: 2 + attempt*2, Kind: "test.run", Status: "running", StartedAt: time.Now().UTC()}
			output, testErr := s.sandbox.Run(runCtx, workspacePath, command.Program, command.Args...)
			testStep.Status = "succeeded"
			if testErr != nil {
				testStep.Status = "failed"
				testStep.Error = testErr.Error()
			}
			testStep.Output = limit(string(output), 12000)
			testStep.FinishedAt = time.Now().UTC()
			if e = s.repo.AddStep(context.Background(), testStep); e != nil {
				return fail(fmt.Errorf("persist test step: %w", e))
			}
			_ = s.repo.Event(runCtx, id, "test.finished", map[string]any{"attempt": attempt + 1, "success": testErr == nil})
			if testErr == nil {
				t.Result = string(output)
				_ = s.repo.Event(runCtx, id, "test.passed", map[string]string{"output": limit(string(output), 12000)})
				break
			}
			t.Result = string(output)
			if attempt == t.MaxRepairAttempts {
				return fail(fmt.Errorf("tests failed after %d repair attempts: %w", attempt, testErr))
			}
			t.Status = StatusRepairing
			if e = s.repo.Update(runCtx, t); e != nil {
				return fail(e)
			}
			repairTask := agentTask
			repairTask.ID = fmt.Sprintf("coding-%s-repair-%d", t.ID, attempt+1)
			repairTask.Prompt = fmt.Sprintf("The previous implementation failed tests. Fix the failures below. Do not weaken or delete tests.\n\nCommand: %s %s\nOutput:\n%s", command.Program, strings.Join(command.Args, " "), limit(string(output), 18000))
			repairStep := Step{TaskID: id, Sequence: 3 + attempt*2, Kind: "agent.repair", Status: "running", StartedAt: time.Now().UTC()}
			if _, e = s.agent.Run(runCtx, repairTask); e != nil {
				repairStep.Status = "failed"
				repairStep.Error = e.Error()
				repairStep.FinishedAt = time.Now().UTC()
				_ = s.repo.AddStep(context.Background(), repairStep)
				return fail(fmt.Errorf("self repair attempt %d: %w", attempt+1, e))
			}
			repairStep.Status = "succeeded"
			repairStep.FinishedAt = time.Now().UTC()
			if e = s.repo.AddStep(runCtx, repairStep); e != nil {
				return fail(fmt.Errorf("persist repair step: %w", e))
			}
			t.RepairAttempts = attempt + 1
		}
	}
	ws = workspace.Workspace{ID: workspaceID, Path: workspacePath, Repository: t.RepositoryPath}
	diff, e := s.worktrees.Diff(runCtx, ws)
	if e != nil {
		return fail(e)
	}
	hash, e := s.worktrees.CommitHash(runCtx, ws)
	if e != nil {
		return fail(e)
	}
	if e = s.repo.SaveSnapshot(runCtx, Snapshot{TaskID: id, Label: "final", CommitHash: hash, Diff: diff}); e != nil {
		return fail(e)
	}
	t.Status = StatusSucceeded
	t.Error = ""
	t.FinishedAt = time.Now().UTC()
	if e = s.repo.Update(runCtx, t); e != nil {
		return fail(e)
	}
	_ = s.repo.Event(runCtx, id, "task.succeeded", map[string]any{"repair_attempts": t.RepairAttempts, "diff_bytes": len(diff)})
	return t, nil
}
func buildPrompt(t Task) string {
	return "You are AgentMesh Coding Agent. Work only inside the assigned git worktree. Inspect the repository, implement the requested change, run relevant checks when practical, and do not modify files outside the workspace. Preserve existing architecture and tests.\n\nTask: " + t.Prompt
}
func detectCommand(path string) Command {
	if _, e := os.Stat(filepath.Join(path, "go.mod")); e == nil {
		return Command{Program: "go", Args: []string{"test", "./..."}}
	}
	if _, e := os.Stat(filepath.Join(path, "package.json")); e == nil {
		return Command{Program: "npm", Args: []string{"test", "--", "--runInBand"}}
	}
	if _, e := os.Stat(filepath.Join(path, "pyproject.toml")); e == nil {
		return Command{Program: "pytest", Args: []string{"-q"}}
	}
	if _, e := os.Stat(filepath.Join(path, "Cargo.toml")); e == nil {
		return Command{Program: "cargo", Args: []string{"test"}}
	}
	return Command{}
}
func limit(v string, n int) string {
	if len(v) > n {
		return v[:n] + "...[truncated]"
	}
	return v
}
func (s *Service) Get(ctx context.Context, id string) (Task, error) { return s.repo.Get(ctx, id) }
func (s *Service) List(ctx context.Context, limit int) ([]Task, error) {
	return s.repo.List(ctx, limit)
}
func (s *Service) Steps(ctx context.Context, id string) ([]Step, error) {
	if _, e := s.repo.Get(ctx, id); e != nil {
		return nil, e
	}
	return s.repo.Steps(ctx, id)
}
func (s *Service) Events(ctx context.Context, id string) ([]Event, error) {
	if _, e := s.repo.Get(ctx, id); e != nil {
		return nil, e
	}
	return s.repo.Events(ctx, id)
}
func (s *Service) Cancel(id string) error {
	s.mu.Lock()
	cancel, ok := s.cancels[id]
	s.mu.Unlock()
	if !ok {
		return ErrConflict
	}
	cancel()
	return nil
}
func (s *Service) Diff(ctx context.Context, id string) (string, error) {
	t, e := s.repo.Get(ctx, id)
	if e != nil {
		return "", e
	}
	if t.WorkspacePath == "" {
		return "", ErrConflict
	}
	return s.worktrees.Diff(ctx, workspace.Workspace{ID: t.WorkspaceID, Path: t.WorkspacePath, Repository: t.RepositoryPath})
}

// lockTask serializes execution and workspace mutation for one task.
func (s *Service) lockTask(id string) func() {
	value, _ := s.workspaceLocks.LoadOrStore(id, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Service) ApplyPatch(ctx context.Context, id, patch string) error {
	unlock := s.lockTask(id)
	defer unlock()
	t, e := s.repo.Get(ctx, id)
	if e != nil {
		return e
	}
	if t.WorkspacePath == "" || len(patch) == 0 || len(patch) > 2<<20 {
		return ErrInvalidTask
	}
	if t.Status == StatusRunning || t.Status == StatusTesting || t.Status == StatusRepairing {
		return ErrConflict
	}
	ws := workspace.Workspace{ID: t.WorkspaceID, Path: t.WorkspacePath, Repository: t.RepositoryPath}
	if e = s.worktrees.ApplyPatch(ctx, ws, patch); e != nil {
		return e
	}
	return s.repo.Event(ctx, id, "patch.applied", map[string]int{"bytes": len(patch)})
}
func (s *Service) Revert(ctx context.Context, id string) error {
	unlock := s.lockTask(id)
	defer unlock()
	t, e := s.repo.Get(ctx, id)
	if e != nil {
		return e
	}
	if t.WorkspacePath == "" || t.Status == StatusRunning || t.Status == StatusTesting || t.Status == StatusRepairing {
		return ErrConflict
	}
	ws := workspace.Workspace{ID: t.WorkspaceID, Path: t.WorkspacePath, Repository: t.RepositoryPath}
	if e = s.worktrees.Reset(ctx, ws); e == nil {
		e = s.repo.Event(ctx, id, "workspace.reverted", nil)
	}
	return e
}
func (s *Service) Cleanup(ctx context.Context, id string) error {
	unlock := s.lockTask(id)
	defer unlock()
	t, e := s.repo.Get(ctx, id)
	if e != nil {
		return e
	}
	if t.WorkspacePath == "" {
		return nil
	}
	if t.Status == StatusRunning || t.Status == StatusTesting || t.Status == StatusRepairing {
		return ErrConflict
	}
	ws := workspace.Workspace{ID: t.WorkspaceID, Path: t.WorkspacePath, Repository: t.RepositoryPath}
	if e = s.worktrees.Remove(ctx, ws); e != nil {
		return e
	}
	_, _ = git(ctx, t.RepositoryPath, "branch", "-D", t.BranchName)
	t.WorkspaceID = ""
	t.WorkspacePath = ""
	t.BranchName = ""
	return s.repo.Update(ctx, t)
}

func (s *Service) Recover(ctx context.Context) (int64, error) {
	return s.repo.RecoverInterrupted(ctx)
}

// Start schedules a task in the background so HTTP request timeouts do not
// truncate long-running coding sessions. Run remains available for controlled
// synchronous use in tests and internal callers.
func (s *Service) Start(ctx context.Context, id string) (Task, error) {
	if err := ctx.Err(); err != nil {
		return Task{}, err
	}
	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return Task{}, err
	}
	if task.Status != StatusPending {
		return Task{}, ErrConflict
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Task{}, ErrConflict
	}
	select {
	case s.maxConcurrent <- struct{}{}:
	default:
		s.mu.Unlock()
		return Task{}, ErrConflict // bounded queue: reject instead of spawning waiters
	}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer func() { <-s.maxConcurrent }()
		_, _ = s.run(s.ctx, id, false)
	}()
	return task, nil
}
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *Service) Snapshots(ctx context.Context, id string) ([]Snapshot, error) {
	if _, err := s.repo.Get(ctx, id); err != nil {
		return nil, err
	}
	return s.repo.Snapshots(ctx, id)
}

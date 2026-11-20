package coding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/agent"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"github.com/thkx/agentmesh/internal/workspace"
	"github.com/thkx/agentmesh/internal/workspace/sandbox/docker"
	"github.com/thkx/agentmesh/internal/workspace/worktree"
)

type Agent interface {
	Run(context.Context, dom.Task) (dom.Result, error)
}
type Service struct {
	repo                     *Repository
	agent                    Agent
	reviewer                 Agent
	artifactPublisher        ArtifactPublisher
	workspaceRoot            string
	allowedRepositoryRoots   []string
	restrictRepositoryAccess bool
	sandbox                  docker.Runner
	worktrees                *worktree.Manager
	taskLocks                *taskLockTable
	closeOnce                sync.Once
	done                     chan struct{}
	mu                       sync.Mutex
	cancels                  map[string]context.CancelFunc
	maxConcurrent            chan struct{}
	ctx                      context.Context
	cancel                   context.CancelFunc
	wg                       sync.WaitGroup
	closed                   bool
	scheduled                map[string]struct{}
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
	return &Service{repo: repo, agent: agent, workspaceRoot: abs, sandbox: docker.Runner{Image: image, NetworkEnabled: network}, worktrees: worktrees, taskLocks: newTaskLockTable(), done: make(chan struct{}), cancels: map[string]context.CancelFunc{}, scheduled: map[string]struct{}{}, maxConcurrent: make(chan struct{}, 2), ctx: serviceCtx, cancel: serviceCancel}, nil
}
func (s *Service) SetReviewer(reviewer Agent) { s.mu.Lock(); s.reviewer = reviewer; s.mu.Unlock() }
func (s *Service) SetArtifactPublisher(publisher ArtifactPublisher) {
	s.mu.Lock()
	s.artifactPublisher = publisher
	s.mu.Unlock()
}

// SetAllowedRepositoryRoots enables deny-by-default repository authorization.
// Every configured root is canonicalized once; Create canonicalizes the
// requested repository again so symlinks cannot escape an approved root.
func (s *Service) SetAllowedRepositoryRoots(roots []string) error {
	canonical := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			return ErrRepositoryNotAllowed
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return fmt.Errorf("resolve coding repository root: %w", err)
		}
		resolved, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return fmt.Errorf("resolve coding repository root %q: %w", root, err)
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("coding repository root %q must be an existing directory", root)
		}
		canonical = append(canonical, filepath.Clean(resolved))
	}
	s.mu.Lock()
	s.allowedRepositoryRoots = canonical
	s.restrictRepositoryAccess = true
	s.mu.Unlock()
	return nil
}

func pathWithinRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Service) Create(ctx context.Context, in CreateRequest) (Task, error) {
	if e := in.Validate(); e != nil {
		return Task{}, e
	}
	repo, e := filepath.Abs(in.RepositoryPath)
	if e != nil {
		return Task{}, e
	}
	repo, e = filepath.EvalSymlinks(repo)
	if e != nil {
		return Task{}, fmt.Errorf("resolve repository path: %w", e)
	}
	info, e := os.Stat(repo)
	if e != nil || !info.IsDir() {
		return Task{}, fmt.Errorf("repository path must be an existing directory")
	}
	top, e := git(ctx, repo, "rev-parse", "--show-toplevel")
	if e != nil {
		return Task{}, fmt.Errorf("repository is not a valid git repository: %w", e)
	}
	top, e = filepath.EvalSymlinks(top)
	if e != nil {
		return Task{}, fmt.Errorf("resolve git repository root: %w", e)
	}
	s.mu.Lock()
	restricted := s.restrictRepositoryAccess
	roots := append([]string(nil), s.allowedRepositoryRoots...)
	s.mu.Unlock()
	if restricted {
		allowed := false
		for _, root := range roots {
			if pathWithinRoot(top, root) {
				allowed = true
				break
			}
		}
		if !allowed {
			return Task{}, ErrRepositoryNotAllowed
		}
	}
	in.RepositoryPath = filepath.Clean(top)
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
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Task{}, ErrConflict
	}
	s.wg.Add(1)
	s.mu.Unlock()
	defer s.wg.Done()
	return s.run(ctx, id, false)
}

func (s *Service) run(ctx context.Context, id string, slotReserved bool) (Task, error) {
	if !slotReserved {
		select {
		case s.maxConcurrent <- struct{}{}:
		case <-ctx.Done():
			return Task{}, ctx.Err()
		case <-s.ctx.Done():
			return Task{}, context.Canceled
		}
	}
	defer func() { <-s.maxConcurrent }()
	unlock := s.taskLocks.Lock(id)
	defer unlock()
	t, e := s.repo.Get(ctx, id)
	if e != nil {
		return Task{}, e
	}
	if t.Status != StatusPending {
		return Task{}, ErrConflict
	}
	claimed, e := s.repo.ClaimWithEvent(ctx, id, "task.started", nil)
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
		if persistErr := s.repo.UpdateWithEvent(context.Background(), t, "task.failed", map[string]string{"error": t.Error}); persistErr != nil {
			err = errors.Join(err, fmt.Errorf("persist failed task and event: %w", persistErr))
		}
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
	if e = s.repo.UpdateWithEvent(runCtx, t, "workspace.created", map[string]string{"path": workspacePath, "branch": branch}); e != nil {
		return fail(fmt.Errorf("persist workspace state/event: %w", e))
	}
	agentTask := dom.Task{ID: "coding-" + t.ID, ModelID: t.ModelID, WorkspaceID: workspaceID, Prompt: buildPrompt(t), MaxSteps: 80, MaxToolCalls: 200, TimeoutSeconds: t.TimeoutSeconds, ApprovedTools: []string{"file.write"}}
	step := Step{TaskID: id, Sequence: 1, Kind: "agent.execute", Status: "running", StartedAt: time.Now().UTC()}
	agentCtx := modelgateway.WithExecutionProvenance(runCtx, t.ExecutionID, t.AttemptID)
	result, agentErr := s.agent.Run(agentCtx, agentTask)
	step.FinishedAt = time.Now().UTC()
	if agentErr != nil {
		step.Status = "failed"
		step.Error = agentErr.Error()
		if persistErr := s.repo.AddStep(context.Background(), step); persistErr != nil {
			agentErr = errors.Join(agentErr, fmt.Errorf("persist failed agent step: %w", persistErr))
		}
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
		if e = s.repo.Event(runCtx, id, "test.skipped", map[string]string{"reason": "no supported project manifest or explicit test command"}); e != nil {
			return fail(fmt.Errorf("persist test skipped event: %w", e))
		}
		s.mu.Lock()
		requireEvidence := s.artifactPublisher != nil
		s.mu.Unlock()
		if requireEvidence {
			return fail(errors.New("no supported test command was detected; configure an explicit test command before coding execution"))
		}
	}
	if command.Program != "" {
		for attempt := 0; attempt <= t.MaxRepairAttempts; attempt++ {
			t.Status = StatusTesting
			t.RepairAttempts = attempt
			if e = s.repo.UpdateWithEvent(runCtx, t, "test.started", map[string]any{"attempt": attempt + 1, "program": command.Program, "args": command.Args}); e != nil {
				return fail(fmt.Errorf("persist test state/event: %w", e))
			}
			testStep := Step{TaskID: id, Sequence: 2 + attempt*2, Kind: "test.run", Status: "running", Input: command.Program + " " + strings.Join(command.Args, " "), StartedAt: time.Now().UTC()}
			output, testErr := s.sandbox.Run(runCtx, workspacePath, command.Program, command.Args...)
			testStep.Status = "succeeded"
			if testErr != nil {
				testStep.Status = "failed"
				testStep.Error = testErr.Error()
			}
			testStep.Output = limit(string(output), 12000)
			testStep.ExitCode = commandExitCode(testErr)
			testStep.FinishedAt = time.Now().UTC()
			testStep.DurationMillis = testStep.FinishedAt.Sub(testStep.StartedAt).Milliseconds()
			if e = s.repo.AddStep(context.Background(), testStep); e != nil {
				return fail(fmt.Errorf("persist test step: %w", e))
			}
			if e = s.repo.Event(runCtx, id, "test.finished", map[string]any{"attempt": attempt + 1, "success": testErr == nil}); e != nil {
				return fail(fmt.Errorf("persist test finished event: %w", e))
			}
			if testErr == nil {
				t.Result = string(output)
				if e = s.repo.Event(runCtx, id, "test.passed", map[string]string{"output": limit(string(output), 12000)}); e != nil {
					return fail(fmt.Errorf("persist test passed event: %w", e))
				}
				break
			}
			t.Result = string(output)
			if attempt == t.MaxRepairAttempts {
				return fail(fmt.Errorf("tests failed after %d repair attempts: %w", attempt, testErr))
			}
			t.Status = StatusRepairing
			if e = s.repo.UpdateWithEvent(runCtx, t, "repair.started", map[string]int{"attempt": attempt + 1}); e != nil {
				return fail(fmt.Errorf("persist repair state/event: %w", e))
			}
			repairTask := agentTask
			repairTask.ID = fmt.Sprintf("coding-%s-repair-%d", t.ID, attempt+1)
			repairTask.Prompt = fmt.Sprintf("The previous implementation failed tests. Fix the failures below. Do not weaken or delete tests.\n\nCommand: %s %s\nOutput:\n%s", command.Program, strings.Join(command.Args, " "), limit(string(output), 18000))
			repairStep := Step{TaskID: id, Sequence: 3 + attempt*2, Kind: "agent.repair", Status: "running", Input: limit(string(output), 12000), StartedAt: time.Now().UTC()}
			repairResult, repairErr := s.agent.Run(modelgateway.WithExecutionProvenance(runCtx, t.ExecutionID, t.AttemptID), repairTask)
			repairStep.FinishedAt = time.Now().UTC()
			repairStep.DurationMillis = repairStep.FinishedAt.Sub(repairStep.StartedAt).Milliseconds()
			if repairErr != nil {
				repairStep.Status = "failed"
				repairStep.Error = repairErr.Error()
				if persistErr := s.repo.AddStep(context.Background(), repairStep); persistErr != nil {
					repairErr = errors.Join(repairErr, fmt.Errorf("persist failed repair step: %w", persistErr))
				}
				return fail(fmt.Errorf("self repair attempt %d: %w", attempt+1, repairErr))
			}
			repairStep.Status = "succeeded"
			repairStep.Output = repairResult.Content
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
	if e = s.review(runCtx, t, diff); e != nil {
		return fail(e)
	}
	hash, e := s.worktrees.CommitHash(runCtx, ws)
	if e != nil {
		return fail(e)
	}
	sum := sha256.Sum256([]byte(diff))
	snapshot := Snapshot{TaskID: id, Label: "final", CommitHash: hash, Diff: diff, ExecutionID: t.ExecutionID, AttemptID: t.AttemptID, ContentSHA256: hex.EncodeToString(sum[:])}
	if e = s.repo.SaveSnapshot(runCtx, snapshot); e != nil {
		return fail(e)
	}
	t.Status = StatusSucceeded
	t.Error = ""
	t.FinishedAt = time.Now().UTC()
	if e = s.repo.UpdateWithEvent(runCtx, t, "task.succeeded", map[string]any{"repair_attempts": t.RepairAttempts, "diff_bytes": len(diff)}); e != nil {
		return fail(fmt.Errorf("persist succeeded task state/event: %w", e))
	}
	if t.ExecutionID == "" {
		if e = s.PublishArtifacts(runCtx, id); e != nil {
			return fail(fmt.Errorf("publish coding artifact evidence: %w", e))
		}
	}
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
	return s.CancelContext(context.Background(), id)
}

// CancelContext cancels either a queued task atomically or an active run through
// its context. A queued cancellation races safely with ClaimWithEvent in SQLite.
func (s *Service) CancelContext(ctx context.Context, id string) error {
	s.mu.Lock()
	cancel, active := s.cancels[id]
	s.mu.Unlock()
	if active {
		cancel()
		return nil
	}

	task, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	if task.Status == StatusPending {
		return s.repo.CancelPending(ctx, id)
	}
	if isActive(task.Status) {
		// The worker may have claimed the task just before its cancel function
		// was published. Recheck once under the service mutex before returning.
		s.mu.Lock()
		cancel, active = s.cancels[id]
		s.mu.Unlock()
		if active {
			cancel()
			return nil
		}
	}
	return ErrConflict
}
func (s *Service) Diff(ctx context.Context, id string) (string, error) {
	unlock, locked := s.taskLocks.TryLock(id)
	if !locked {
		return "", ErrConflict
	}
	defer unlock()
	t, e := s.repo.Get(ctx, id)
	if e != nil {
		return "", e
	}
	if isActive(t.Status) {
		return "", ErrConflict
	}
	if t.WorkspacePath == "" {
		return "", ErrConflict
	}
	return s.worktrees.Diff(ctx, workspace.Workspace{ID: t.WorkspaceID, Path: t.WorkspacePath, Repository: t.RepositoryPath})
}
func (s *Service) ApplyPatch(ctx context.Context, id, patch string) error {
	unlock, locked := s.taskLocks.TryLock(id)
	if !locked {
		return ErrConflict
	}
	defer unlock()
	t, e := s.repo.Get(ctx, id)
	if e != nil {
		return e
	}
	if isActive(t.Status) {
		return ErrConflict
	}
	if t.WorkspacePath == "" || len(patch) == 0 || len(patch) > 2<<20 {
		return ErrInvalidTask
	}
	ws := workspace.Workspace{ID: t.WorkspaceID, Path: t.WorkspacePath, Repository: t.RepositoryPath}
	if e = s.worktrees.ApplyPatch(ctx, ws, patch); e != nil {
		return e
	}
	return s.repo.Event(ctx, id, "patch.applied", map[string]int{"bytes": len(patch)})
}
func (s *Service) Revert(ctx context.Context, id string) error {
	unlock, locked := s.taskLocks.TryLock(id)
	if !locked {
		return ErrConflict
	}
	defer unlock()
	t, e := s.repo.Get(ctx, id)
	if e != nil {
		return e
	}
	if isActive(t.Status) {
		return ErrConflict
	}
	if t.WorkspacePath == "" {
		return ErrConflict
	}
	ws := workspace.Workspace{ID: t.WorkspaceID, Path: t.WorkspacePath, Repository: t.RepositoryPath}
	if e = s.worktrees.Reset(ctx, ws); e == nil {
		e = s.repo.Event(ctx, id, "workspace.reverted", nil)
	}
	return e
}
func (s *Service) Cleanup(ctx context.Context, id string) error {
	unlock, locked := s.taskLocks.TryLock(id)
	if !locked {
		return ErrConflict
	}
	defer unlock()
	t, e := s.repo.Get(ctx, id)
	if e != nil {
		return e
	}
	if isActive(t.Status) {
		return ErrConflict
	}
	if t.WorkspacePath == "" {
		return nil
	}
	ws := workspace.Workspace{ID: t.WorkspaceID, Path: t.WorkspacePath, Repository: t.RepositoryPath}
	if e = s.worktrees.Remove(ctx, ws); e != nil {
		return e
	}
	if _, branchErr := git(ctx, t.RepositoryPath, "branch", "-D", t.BranchName); branchErr != nil {
		return fmt.Errorf("delete coding branch: %w", branchErr)
	}
	t.WorkspaceID = ""
	t.WorkspacePath = ""
	t.BranchName = ""
	return s.repo.UpdateWithEvent(ctx, t, "workspace.cleaned", nil)
}

func (s *Service) Recover(ctx context.Context) (int64, error) { return s.repo.RecoverInterrupted(ctx) }

func (s *Service) BindExecution(ctx context.Context, taskID, executionID, attemptID string) error {
	return s.repo.BindExecution(ctx, taskID, executionID, attemptID)
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
	// The pending task is durable in SQLite. Schedule it once in this process;
	// the worker waits for a concurrency slot instead of rejecting the task.
	if _, exists := s.scheduled[id]; exists {
		s.mu.Unlock()
		return task, nil
	}
	s.scheduled[id] = struct{}{}
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer func() { s.mu.Lock(); delete(s.scheduled, id); s.mu.Unlock() }()
		_, _ = s.run(s.ctx, id, false)
	}()
	return task, nil
}

// CloseContext cancels all service-owned runs and waits up to the caller's deadline.
func (s *Service) CloseContext(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()
	s.closeOnce.Do(func() { go func() { s.wg.Wait(); close(s.done) }() })
	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = s.CloseContext(ctx)
}

func isActive(status Status) bool {
	return status == StatusRunning || status == StatusTesting || status == StatusRepairing
}

func (s *Service) Snapshots(ctx context.Context, id string) ([]Snapshot, error) {
	if _, err := s.repo.Get(ctx, id); err != nil {
		return nil, err
	}
	return s.repo.Snapshots(ctx, id)
}

func (s *Service) PublishArtifacts(ctx context.Context, taskID string) error {
	task, err := s.repo.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if task.Status != StatusSucceeded {
		return ErrConflict
	}
	s.mu.Lock()
	publisher := s.artifactPublisher
	s.mu.Unlock()
	if publisher == nil {
		return nil
	}
	events, err := s.repo.Events(ctx, taskID)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.Type == "artifact.published" {
			return nil
		}
	}
	snapshots, err := s.repo.Snapshots(ctx, taskID)
	if err != nil {
		return err
	}
	if len(snapshots) == 0 {
		return errors.New("coding task has no final snapshot")
	}
	snapshot := snapshots[len(snapshots)-1]
	steps, err := s.repo.Steps(ctx, taskID)
	if err != nil {
		return err
	}
	publication, err := publisher.PublishCodingArtifact(ctx, task, snapshot, steps)
	if err != nil {
		return err
	}
	return s.repo.Event(ctx, taskID, "artifact.published", publication)
}

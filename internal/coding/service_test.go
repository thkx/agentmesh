package coding

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	dom "github.com/thkx/agentmesh/internal/domain/agent"
	"github.com/thkx/agentmesh/internal/workspace"
)

type fakeCodingAgent struct{ workspaces *workspace.Manager }

func (f fakeCodingAgent) Run(ctx context.Context, task dom.Task) (dom.Result, error) {
	ws, err := f.workspaces.Resolve(task.WorkspaceID)
	if err != nil {
		return dom.Result{}, err
	}
	if err = os.WriteFile(filepath.Join(ws.Path, "agent-created.txt"), []byte("implemented\\n"), 0600); err != nil {
		return dom.Result{}, err
	}
	return dom.Result{TaskID: task.ID, Status: dom.StatusSucceeded, Content: "implemented requested change"}, nil
}
func TestCodingTaskEndToEndWithoutDockerTests(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "agentmesh-test@example.invalid")
	run("config", "user.name", "AgentMesh Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	root := filepath.Join(t.TempDir(), "workspaces")
	wm, err := workspace.NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(testRepository(t), fakeCodingAgent{workspaces: wm}, root, "golang:1.27.3", false)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	task, err := service.Create(context.Background(), CreateRequest{ID: "task-e2e", RepositoryPath: repo, ModelID: "test-model", Prompt: "create a file"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Run(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusSucceeded {
		t.Fatalf("status = %s, error=%s", result.Status, result.Error)
	}
	diff, err := service.Diff(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "agent-created.txt") {
		t.Fatalf("created file missing from diff: %s", diff)
	}
	steps, err := service.Steps(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Status != "succeeded" {
		t.Fatalf("unexpected steps: %#v", steps)
	}
	snapshots, err := service.Snapshots(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 || !strings.Contains(snapshots[0].Diff, "agent-created.txt") {
		t.Fatalf("unexpected snapshots: %#v", snapshots)
	}
	if err = service.Cleanup(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	cleaned, err := service.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cleaned.WorkspacePath != "" {
		t.Fatalf("workspace was not cleaned: %#v", cleaned)
	}
}

type blockingCodingAgent struct {
	workspaces *workspace.Manager
	started    chan struct{}
	calls      atomic.Int32
}

func (f *blockingCodingAgent) Run(ctx context.Context, task dom.Task) (dom.Result, error) {
	ws, err := f.workspaces.Resolve(task.WorkspaceID)
	if err != nil {
		return dom.Result{}, err
	}
	if err = os.WriteFile(filepath.Join(ws.Path, "agent-created.txt"), []byte("work\n"), 0600); err != nil {
		return dom.Result{}, err
	}
	f.calls.Add(1)
	f.started <- struct{}{}
	<-ctx.Done()
	return dom.Result{}, ctx.Err()
}

func makeGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "agentmesh-test@example.invalid")
	run("config", "user.name", "AgentMesh Test")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	return repo
}

func TestStartReservesBoundedSlotsAndCloseCancelsRuns(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	repoPath := makeGitRepo(t)
	root := filepath.Join(t.TempDir(), "workspaces")
	wm, err := workspace.NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	agent := &blockingCodingAgent{workspaces: wm, started: make(chan struct{}, 3)}
	service, err := NewService(testRepository(t), agent, root, "golang:1.27.3", false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 3; i++ {
		if _, err = service.Create(context.Background(), CreateRequest{ID: fmt.Sprintf("start-%d", i), RepositoryPath: repoPath, ModelID: "test", Prompt: "test"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = service.Start(context.Background(), "start-1"); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Start(context.Background(), "start-2"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-agent.started:
	case <-time.After(10 * time.Second):
		t.Fatal("first agent did not start")
	}
	select {
	case <-agent.started:
	case <-time.After(10 * time.Second):
		t.Fatal("second agent did not start")
	}
	if _, err = service.Start(context.Background(), "start-3"); err != nil {
		t.Fatalf("third task should remain queued at capacity: %v", err)
	}
	queued, err := service.Get(context.Background(), "start-3")
	if err != nil || queued.Status != StatusPending { t.Fatalf("third task should remain durably pending: task=%#v err=%v", queued, err) }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = service.CloseContext(ctx); err != nil {
		t.Fatalf("close did not cancel and wait for runs: %v", err)
	}
}

func TestTaskMutationRejectsRunningTask(t *testing.T) {
	repo := testRepository(t)
	task, err := repo.Create(context.Background(), CreateRequest{ID: "running", RepositoryPath: "/tmp/repo", ModelID: "test", Prompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	task.Status = StatusRunning
	if err = repo.Update(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repo, fakeCodingAgent{}, t.TempDir(), "golang:1.27.3", false)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err = service.ApplyPatch(context.Background(), task.ID, "diff --git a/a b/a\n"); !errors.Is(err, ErrConflict) {
		t.Fatalf("patch should reject active task: %v", err)
	}
	if err = service.Revert(context.Background(), task.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("revert should reject active task: %v", err)
	}
	if err = service.Cleanup(context.Background(), task.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("cleanup should reject active task: %v", err)
	}
}

func TestCancelContextCancelsQueuedTaskBeforeWorkerClaim(t *testing.T) {
	repo := testRepository(t)
	ctx := context.Background()
	task, err := repo.Create(ctx, CreateRequest{
		ID: "cancel-service-pending", RepositoryPath: "/tmp/repo",
		ModelID: "test-model", Prompt: "do work",
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repo, fakeCodingAgent{}, t.TempDir(), "golang:1.27.3", false)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err := service.CancelContext(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusCanceled {
		t.Fatalf("status=%s, want canceled", got.Status)
	}
	claimed, err := repo.Claim(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed {
		t.Fatal("canceled task was claimable")
	}
}

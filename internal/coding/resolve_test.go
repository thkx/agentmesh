package coding

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func markTaskInterrupted(t *testing.T, service *Service, task Task) Task {
	t.Helper()
	task.Status = StatusInterrupted
	task.OutcomeUnknown = true
	task.StartedAt = time.Now().Add(-time.Minute)
	task.FinishedAt = time.Now().UTC()
	if err := service.repo.Update(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := service.repo.UpdateWithEvent(context.Background(), task, "task.interrupted", map[string]string{"reason": "test interruption"}); err != nil {
		t.Fatal(err)
	}
	updated, err := service.Get(context.Background(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func TestResolveInterruptedKeepIsExplicitAndIdempotent(t *testing.T) {
	repo := makeGitRepo(t)
	service, err := NewService(testRepository(t), fakeCodingAgent{}, t.TempDir(), "golang:1.27.3", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetAllowedRepositoryRoots([]string{repo}); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	task, err := service.Create(context.Background(), CreateRequest{
		ID: "resolve-keep", RepositoryPath: repo, ModelID: "test", Prompt: "inspect",
	})
	if err != nil {
		t.Fatal(err)
	}
	task = markTaskInterrupted(t, service, task)

	resolved, err := service.ResolveInterrupted(context.Background(), task.ID, "keep", "I inspected the diff and retained the Worktree.")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != StatusFailed || resolved.OutcomeUnknown || !strings.HasPrefix(resolved.Error, manualResolutionErrorPrefix+"keep:") {
		t.Fatalf("unexpected keep resolution: %+v", resolved)
	}
	again, err := service.ResolveInterrupted(context.Background(), task.ID, "keep", "I inspected the diff and retained the Worktree.")
	if err != nil || again.Status != StatusFailed {
		t.Fatalf("same resolution must be idempotent: task=%+v err=%v", again, err)
	}
	if _, err = service.ResolveInterrupted(context.Background(), task.ID, "discard", "different decision"); err != ErrConflict {
		t.Fatalf("conflicting resolution must be rejected, got %v", err)
	}
}

func TestResolveInterruptedDiscardResetsWorktree(t *testing.T) {
	repo := makeGitRepo(t)
	root := t.TempDir()
	service, err := NewService(testRepository(t), fakeCodingAgent{}, root, "golang:1.27.3", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetAllowedRepositoryRoots([]string{repo}); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	task, err := service.Create(context.Background(), CreateRequest{
		ID: "resolve-discard", RepositoryPath: repo, ModelID: "test", Prompt: "inspect",
	})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := service.worktrees.Create(context.Background(), repo, "coding-"+task.ID)
	if err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(ws.Path, "partial-change.txt")
	if err = os.WriteFile(untracked, []byte("partial\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task.WorkspaceID = ws.ID
	task.WorkspacePath = ws.Path
	task = markTaskInterrupted(t, service, task)

	resolved, err := service.ResolveInterrupted(context.Background(), task.ID, "discard", "I inspected the partial work and discarded it.")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != StatusCanceled || resolved.OutcomeUnknown {
		t.Fatalf("unexpected discard resolution: %+v", resolved)
	}
	if _, err = os.Stat(untracked); !os.IsNotExist(err) {
		t.Fatalf("discard did not reset the Worktree: stat err=%v", err)
	}
}

func TestRecoverReattachesWorktreeCreatedBeforeTaskMetadataPersisted(t *testing.T) {
	ctx := context.Background()
	repoPath := makeGitRepo(t)
	root := t.TempDir()
	service, err := NewService(testRepository(t), fakeCodingAgent{}, root, "golang:1.27.3", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetAllowedRepositoryRoots([]string{repoPath}); err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	task, err := service.Create(ctx, CreateRequest{
		ID: "recover-worktree", RepositoryPath: repoPath, ModelID: "test", Prompt: "inspect partial work",
	})
	if err != nil {
		t.Fatal(err)
	}
	workspaceID := "coding-" + task.ID
	branch := "agentmesh/" + task.ID
	ws, err := service.worktrees.CreateBranch(ctx, repoPath, workspaceID, branch)
	if err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(ws.Path, "partial-change.txt")
	if err = os.WriteFile(partial, []byte("partial work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	task.Status = StatusRunning
	task.StartedAt = time.Now().UTC()
	if err = service.repo.Update(ctx, task); err != nil {
		t.Fatal(err)
	}

	recovered, err := service.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if recovered != 1 {
		t.Fatalf("recovered count=%d, want 1", recovered)
	}
	got, err := service.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusInterrupted || !got.OutcomeUnknown || got.WorkspaceID != workspaceID || got.WorkspacePath != ws.Path || got.BranchName != branch {
		events, eventsErr := service.Events(ctx, task.ID)
		t.Fatalf("interrupted task Worktree was not reattached: task=%+v events=%+v events_err=%v", got, events, eventsErr)
	}
	diff, err := service.Diff(ctx, task.ID)
	if err != nil || !strings.Contains(diff, "partial-change.txt") {
		t.Fatalf("recovered Worktree diff missing: err=%v diff=%s", err, diff)
	}
	events, err := service.Events(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		if event.Type == "workspace.recovered" {
			found = true
		}
	}
	if !found {
		t.Fatal("workspace.recovered event was not persisted")
	}
}

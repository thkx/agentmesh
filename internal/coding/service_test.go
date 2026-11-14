package coding

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

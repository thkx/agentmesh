package coding

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func initGitRepositoryAt(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = path
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	run("init")
	run("config", "user.name", "AgentMesh Test")
	run("config", "user.email", "agentmesh-test@example.invalid")
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	return path
}

func TestCreateEnforcesCanonicalRepositoryAllowlist(t *testing.T) {
	allowedRoot := t.TempDir()
	allowedRepo := initGitRepositoryAt(t, filepath.Join(allowedRoot, "repo"))
	outsideRepo := makeGitRepo(t)

	service, err := NewService(testRepository(t), fakeCodingAgent{}, t.TempDir(), "golang:1.27.3", false)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err = service.SetAllowedRepositoryRoots([]string{allowedRoot}); err != nil {
		t.Fatal(err)
	}

	if _, err = service.Create(context.Background(), CreateRequest{
		ID: "allowed-repo", RepositoryPath: allowedRepo, ModelID: "test", Prompt: "inspect",
	}); err != nil {
		t.Fatalf("repository beneath approved root should be accepted: %v", err)
	}
	if _, err = service.Create(context.Background(), CreateRequest{
		ID: "outside-repo", RepositoryPath: outsideRepo, ModelID: "test", Prompt: "inspect",
	}); !errors.Is(err, ErrRepositoryNotAllowed) {
		t.Fatalf("repository outside approved roots should be rejected, got %v", err)
	}

	link := filepath.Join(allowedRoot, "repository-symlink")
	if err = os.Symlink(outsideRepo, link); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Create(context.Background(), CreateRequest{
		ID: "symlink-escape", RepositoryPath: link, ModelID: "test", Prompt: "inspect",
	}); !errors.Is(err, ErrRepositoryNotAllowed) {
		t.Fatalf("symlink escape should be rejected, got %v", err)
	}
}

func TestEmptyRepositoryAllowlistDisablesCodingTasks(t *testing.T) {
	repo := makeGitRepo(t)
	service, err := NewService(testRepository(t), fakeCodingAgent{}, t.TempDir(), "golang:1.27.3", false)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if err = service.SetAllowedRepositoryRoots(nil); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Create(context.Background(), CreateRequest{
		ID: "disabled-coding", RepositoryPath: repo, ModelID: "test", Prompt: "inspect",
	}); !errors.Is(err, ErrRepositoryNotAllowed) {
		t.Fatalf("empty allowlist should disable coding tasks, got %v", err)
	}
}

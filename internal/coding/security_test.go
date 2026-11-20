package coding

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateEnforcesCanonicalRepositoryAllowlist(t *testing.T) {
	allowedRepo := makeGitRepo(t)
	allowedRoot := filepath.Dir(allowedRepo)
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

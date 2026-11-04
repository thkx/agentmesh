package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/thkx/agentmesh/internal/workspace"
)

type Manager struct{ root string }

func New(root string) (*Manager, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("worktree root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &Manager{root: abs}, nil
}
func (m *Manager) Create(ctx context.Context, repo, id string) (workspace.Workspace, error) {
	if !validID(id) {
		return workspace.Workspace{}, workspace.ErrInvalidWorkspaceID
	}
	if err := os.MkdirAll(m.root, 0700); err != nil {
		return workspace.Workspace{}, err
	}
	repoAbs, err := filepath.Abs(repo)
	if err != nil {
		return workspace.Workspace{}, err
	}
	path := filepath.Join(m.root, id)
	if err := exec.CommandContext(ctx, "git", "-C", repoAbs, "rev-parse", "--show-toplevel").Run(); err != nil {
		return workspace.Workspace{}, fmt.Errorf("repository is not a valid git worktree: %w", err)
	}
	if err := exec.CommandContext(ctx, "git", "-C", repoAbs, "worktree", "add", "--detach", path, "HEAD").Run(); err != nil {
		return workspace.Workspace{}, err
	}
	return workspace.Workspace{ID: id, Path: path, Repository: repoAbs}, nil
}
func (m *Manager) Remove(ctx context.Context, w workspace.Workspace) error {
	if !validID(w.ID) {
		return workspace.ErrInvalidWorkspaceID
	}
	expected := filepath.Join(m.root, w.ID)
	actual, err := filepath.Abs(w.Path)
	if err != nil || actual != expected {
		return fmt.Errorf("workspace path does not match worktree id")
	}
	if w.Repository == "" {
		return fmt.Errorf("repository is required to remove worktree")
	}
	if err := exec.CommandContext(ctx, "git", "-C", w.Repository, "worktree", "remove", "--force", w.Path).Run(); err != nil {
		return err
	}
	return nil
}
func validID(id string) bool {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\") {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

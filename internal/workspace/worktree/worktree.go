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

// Inspect safely reattaches a deterministic Worktree path after a process
// restart. It verifies the path remains beneath the managed root and belongs
// to the expected repository before returning its branch.
func (m *Manager) Inspect(ctx context.Context, repo, id string) (workspace.Workspace, string, error) {
	if !validID(id) {
		return workspace.Workspace{}, "", workspace.ErrInvalidWorkspaceID
	}
	rootResolved, err := filepath.EvalSymlinks(m.root)
	if err != nil {
		return workspace.Workspace{}, "", err
	}
	candidate := filepath.Join(m.root, id)
	actual, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return workspace.Workspace{}, "", err
	}
	expected := filepath.Join(rootResolved, id)
	if actual != expected {
		return workspace.Workspace{}, "", fmt.Errorf("worktree path is outside the managed root")
	}
	repoAbs, err := filepath.Abs(repo)
	if err != nil {
		return workspace.Workspace{}, "", err
	}
	repoAbs, err = filepath.EvalSymlinks(repoAbs)
	if err != nil {
		return workspace.Workspace{}, "", err
	}
	worktreeRoot, err := gitOutput(ctx, actual, "rev-parse", "--show-toplevel")
	if err != nil {
		return workspace.Workspace{}, "", err
	}
	worktreeRoot, err = filepath.EvalSymlinks(worktreeRoot)
	if err != nil || worktreeRoot != actual {
		return workspace.Workspace{}, "", fmt.Errorf("worktree root does not match its managed path")
	}
	worktreeCommon, err := gitCommonDir(ctx, actual)
	if err != nil {
		return workspace.Workspace{}, "", err
	}
	repoCommon, err := gitCommonDir(ctx, repoAbs)
	if err != nil {
		return workspace.Workspace{}, "", err
	}
	// macOS can expose the same directory through /var and /private/var
	// paths. Comparing cleaned strings rejects a valid linked Worktree when
	// Git reports those aliases differently after a restart. Compare the
	// canonical filesystem objects instead; this still rejects a Worktree
	// whose git-common-dir belongs to another repository.
	worktreeCommonInfo, worktreeStatErr := os.Stat(worktreeCommon)
	repoCommonInfo, repoStatErr := os.Stat(repoCommon)
	if worktreeStatErr != nil || repoStatErr != nil || !os.SameFile(worktreeCommonInfo, repoCommonInfo) {
		return workspace.Workspace{}, "", fmt.Errorf("worktree belongs to a different repository")
	}
	branch, err := gitOutput(ctx, actual, "branch", "--show-current")
	if err != nil || branch == "" {
		return workspace.Workspace{}, "", fmt.Errorf("worktree branch could not be verified")
	}
	ws := workspace.Workspace{ID: id, Path: actual, Repository: repoAbs}
	if err = m.validateWorkspace(ws); err != nil {
		return workspace.Workspace{}, "", err
	}
	return ws, branch, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func gitCommonDir(ctx context.Context, dir string) (string, error) {
	common, err := gitOutput(ctx, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	resolved, err := filepath.EvalSymlinks(common)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func (m *Manager) Remove(ctx context.Context, w workspace.Workspace) error {
	if err := m.validateWorkspace(w); err != nil {
		return err
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

// CreateBranch creates an isolated worktree from the repository's current HEAD
// and checks the requested branch name with Git before creating it.
func (m *Manager) CreateBranch(ctx context.Context, repo, id, branch string) (workspace.Workspace, error) {
	if !validID(id) {
		return workspace.Workspace{}, workspace.ErrInvalidWorkspaceID
	}
	if strings.TrimSpace(branch) == "" {
		return workspace.Workspace{}, fmt.Errorf("branch is required")
	}
	repoAbs, err := filepath.Abs(repo)
	if err != nil {
		return workspace.Workspace{}, err
	}
	if err := os.MkdirAll(m.root, 0700); err != nil {
		return workspace.Workspace{}, err
	}
	path := filepath.Join(m.root, id)
	if _, err := os.Stat(path); err == nil {
		return workspace.Workspace{}, fmt.Errorf("worktree path already exists")
	} else if !os.IsNotExist(err) {
		return workspace.Workspace{}, fmt.Errorf("inspect worktree path: %w", err)
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", repoAbs, "check-ref-format", "--branch", branch).CombinedOutput(); err != nil {
		return workspace.Workspace{}, fmt.Errorf("invalid branch name: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", repoAbs, "worktree", "add", "-b", branch, path, "HEAD").CombinedOutput(); err != nil {
		return workspace.Workspace{}, fmt.Errorf("create worktree: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return workspace.Workspace{ID: id, Path: path, Repository: repoAbs}, nil
}
func (m *Manager) Diff(ctx context.Context, w workspace.Workspace) (string, error) {
	if err := m.validateWorkspace(w); err != nil {
		return "", err
	}
	// Include untracked files as intent-to-add so newly created source files
	// are visible in the review patch without staging their contents.
	if out, err := exec.CommandContext(ctx, "git", "-C", w.Path, "add", "-N", "--", ".").CombinedOutput(); err != nil {
		return "", fmt.Errorf("include untracked files in diff: %w: %s", err, strings.TrimSpace(string(out)))
	}
	out, err := exec.CommandContext(ctx, "git", "-C", w.Path, "diff", "--binary", "HEAD").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
func (m *Manager) ApplyPatch(ctx context.Context, w workspace.Workspace, patch string) error {
	if err := m.validateWorkspace(w); err != nil {
		return err
	}
	check := exec.CommandContext(ctx, "git", "-C", w.Path, "apply", "--check", "-")
	check.Stdin = strings.NewReader(patch)
	if out, err := check.CombinedOutput(); err != nil {
		return fmt.Errorf("patch check: %w: %s", err, strings.TrimSpace(string(out)))
	}
	apply := exec.CommandContext(ctx, "git", "-C", w.Path, "apply", "-")
	apply.Stdin = strings.NewReader(patch)
	if out, err := apply.CombinedOutput(); err != nil {
		return fmt.Errorf("apply patch: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
func (m *Manager) Reset(ctx context.Context, w workspace.Workspace) error {
	if err := m.validateWorkspace(w); err != nil {
		return err
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", w.Path, "reset", "--hard", "HEAD").CombinedOutput(); err != nil {
		return fmt.Errorf("git reset: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", w.Path, "clean", "-fd").CombinedOutput(); err != nil {
		return fmt.Errorf("git clean: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
func (m *Manager) CommitHash(ctx context.Context, w workspace.Workspace) (string, error) {
	if err := m.validateWorkspace(w); err != nil {
		return "", err
	}
	out, err := exec.CommandContext(ctx, "git", "-C", w.Path, "rev-parse", "HEAD").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}
func (m *Manager) validateWorkspace(w workspace.Workspace) error {
	if !validID(w.ID) {
		return workspace.ErrInvalidWorkspaceID
	}
	rootResolved, err := filepath.EvalSymlinks(m.root)
	if err != nil {
		return err
	}
	expected := filepath.Join(rootResolved, w.ID)
	actual, err := filepath.Abs(w.Path)
	if err != nil {
		return err
	}
	actualResolved, err := filepath.EvalSymlinks(actual)
	if err != nil {
		return err
	}
	if actualResolved != expected {
		return fmt.Errorf("workspace path does not match worktree id")
	}
	info, err := os.Stat(actualResolved)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace is not a directory")
	}
	if strings.TrimSpace(w.Repository) == "" {
		return fmt.Errorf("repository is required")
	}
	return nil
}

package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateBranchDiffPatchAndReset(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	manager, err := New(filepath.Join(t.TempDir(), "worktrees"))
	if err != nil {
		t.Fatal(err)
	}
	w, err := manager.CreateBranch(context.Background(), repo, "task-1", "agentmesh/task-1")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(w.Path, "new.txt"), []byte("new file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	diff, err := manager.Diff(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "new.txt") {
		t.Fatalf("untracked file missing from diff: %s", diff)
	}
	patch := "diff --git a/README.md b/README.md\n--- a/README.md\n+++ b/README.md\n@@ -1 +1 @@\n-base\n+patched\n"
	if err = manager.ApplyPatch(context.Background(), w, patch); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(w.Path, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "patched\n" {
		t.Fatalf("patch content = %q", content)
	}
	if err = manager.Reset(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(w.Path, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("untracked file remained after reset: %v", err)
	}
	content, err = os.ReadFile(filepath.Join(w.Path, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "base\n" {
		t.Fatalf("reset content = %q", content)
	}
	if err = manager.Remove(context.Background(), w); err != nil {
		t.Fatal(err)
	}
}

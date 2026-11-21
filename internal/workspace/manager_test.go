package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestManagerCreateRemoveAndTraversal(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(context.Background(), "../escape"); !errors.Is(err, ErrInvalidWorkspaceID) {
		t.Fatalf("expected invalid id: %v", err)
	}
	w, err := m.Create(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(w.ID); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceLeasePreventsRemoval(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := m.Create(context.Background(), "leased")
	if err != nil {
		t.Fatal(err)
	}
	release, err := m.AcquirePath(w.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(w.ID); !errors.Is(err, ErrWorkspaceInUse) {
		t.Fatalf("expected workspace in-use error, got %v", err)
	}
	release()
	if err := m.Remove(w.ID); err != nil {
		t.Fatal(err)
	}
}

func TestManagerEnsureCreatesAndReusesWorkspace(t *testing.T) {
	m, err := NewManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Ensure(context.Background(), "persisted-workspace")
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Ensure(context.Background(), "persisted-workspace")
	if err != nil {
		t.Fatal(err)
	}
	if first.Path != second.Path {
		t.Fatalf("expected stable path, got %q and %q", first.Path, second.Path)
	}
	if _, err := m.Resolve("persisted-workspace"); err != nil {
		t.Fatalf("ensured workspace should resolve: %v", err)
	}
}

func TestManagerEnsureRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	m, err := NewManager(root)
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Ensure(context.Background(), "linked"); !errors.Is(err, ErrInvalidWorkspaceID) {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

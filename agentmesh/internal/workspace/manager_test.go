package workspace

import (
	"context"
	"errors"
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

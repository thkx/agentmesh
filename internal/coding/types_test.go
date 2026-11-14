package coding

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateRequestValidation(t *testing.T) {
	base := CreateRequest{ID: "task-1", RepositoryPath: "/tmp/repo", ModelID: "model", Prompt: "implement feature"}
	if err := base.Validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	bad := base
	bad.ID = "../escape"
	if !errors.Is(bad.Validate(), ErrInvalidTask) {
		t.Fatal("path-like id accepted")
	}
	bad = base
	bad.MaxRepairAttempts = 6
	if !errors.Is(bad.Validate(), ErrInvalidTask) {
		t.Fatal("repair limit accepted")
	}
}
func TestValidateCommand(t *testing.T) {
	if err := ValidateCommand(Command{Program: "go", Args: []string{"test", "./..."}}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Command{{Program: "sh", Args: []string{"-c", "rm -rf /"}}, {Program: "go", Args: []string{"test;touch /tmp/pwn"}}, {Program: "docker"}} {
		if !errors.Is(ValidateCommand(c), ErrUnsafeCommand) {
			t.Fatalf("unsafe command accepted: %#v", c)
		}
	}
}
func TestDetectCommand(t *testing.T) {
	t.Run("go", func(t *testing.T) {
		d := t.TempDir()
		if err := os.WriteFile(filepath.Join(d, "go.mod"), []byte("module example"), 0600); err != nil {
			t.Fatal(err)
		}
		c := detectCommand(d)
		if c.Program != "go" || len(c.Args) != 2 {
			t.Fatalf("unexpected command: %#v", c)
		}
	})
}

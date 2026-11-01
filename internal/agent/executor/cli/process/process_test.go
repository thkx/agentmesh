package process

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"testing"
)

func TestRunnerCapsOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("head fixture is Unix-only")
	}
	head, err := exec.LookPath("head")
	if err != nil {
		t.Skip("head is not installed")
	}
	_, err = (Runner{}).Run(context.Background(), Spec{Path: head, Args: []string{"-c", "9000000", "/dev/zero"}})
	if !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("expected output limit error, got %v", err)
	}
}

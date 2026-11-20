package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunnerProvidesEphemeralBuildCachesWithoutRelaxingReadOnlyRoot(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(root, "docker-args.txt")
	bin := filepath.Join(root, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + capture + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := Runner{Image: "golang:1.27.3", DockerPath: bin}
	if _, err := runner.Run(context.Background(), workspace, "go", "test", "./..."); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"--read-only",
		"--network\nnone",
		"/go/pkg/mod:rw,nosuid,size=1g",
		"/root/.cargo:rw,nosuid,size=1g",
		"GOMODCACHE=/go/pkg/mod",
		"CARGO_HOME=/root/.cargo",
		"/workspace:rw",
	} {
		if !strings.Contains(string(args), expected) {
			t.Fatalf("Docker invocation is missing %q:\n%s", expected, args)
		}
	}
}

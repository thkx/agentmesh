package docker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Runner executes a command in an isolated Docker container. The workspace is
// mounted at /workspace; networking is disabled unless explicitly enabled.
type Runner struct {
	Image          string
	NetworkEnabled bool
	DockerPath     string
}

func (r Runner) Run(ctx context.Context, workspace string, command string, args ...string) ([]byte, error) {
	if strings.TrimSpace(r.Image) == "" {
		return nil, fmt.Errorf("docker sandbox image is required")
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return nil, fmt.Errorf("sandbox workspace: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("sandbox workspace must be a directory")
	}
	bin := r.DockerPath
	if bin == "" {
		bin = "docker"
	}
	network := "none"
	if r.NetworkEnabled {
		network = "bridge"
	}
	argv := []string{"run", "--rm", "--network", network, "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=128", "--memory=2g", "--cpus=2", "--tmpfs", "/tmp:rw,exec,nosuid,size=512m",
		"--tmpfs", "/root/.cache:rw,nosuid,size=512m",
		"--tmpfs", "/root/.npm:rw,nosuid,size=256m",
		"--tmpfs", "/root/.cargo:rw,nosuid,size=1g",
		"--tmpfs", "/go/pkg/mod:rw,nosuid,size=1g",
		"--env", "GOCACHE=/tmp/go-build-cache",
		"--env", "GOMODCACHE=/go/pkg/mod",
		"--env", "CARGO_HOME=/root/.cargo",
		"--ulimit", "nofile=1024:1024",
		"--ipc", "none",
		"-v", workspace + ":/workspace:rw", "-w", "/workspace", r.Image, command}
	argv = append(argv, args...)
	cmd := exec.CommandContext(ctx, bin, argv...)
	output := &limitedBuffer{limit: 4 << 20}
	cmd.Stdout = output
	cmd.Stderr = output
	err = cmd.Run()
	out := output.Bytes()
	if err != nil {
		return out, fmt.Errorf("docker sandbox: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

type limitedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
	limit  int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buffer.Write(p[:remaining])
		} else {
			_, _ = b.buffer.Write(p)
		}
	}
	return len(p), nil
}
func (b *limitedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buffer.Bytes()...)
}

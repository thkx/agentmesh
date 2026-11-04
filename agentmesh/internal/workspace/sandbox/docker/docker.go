package docker

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
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
	bin := r.DockerPath
	if bin == "" {
		bin = "docker"
	}
	network := "none"
	if r.NetworkEnabled {
		network = "bridge"
	}
	argv := []string{"run", "--rm", "--network", network, "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=64m", "-v", workspace + ":/workspace:rw", "-w", "/workspace", r.Image, command}
	argv = append(argv, args...)
	out, err := exec.CommandContext(ctx, bin, argv...).CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("docker sandbox: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

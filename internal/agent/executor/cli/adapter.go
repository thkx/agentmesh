package cli

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/thkx/agentmesh/internal/agent/executor/cli/claude"
	"github.com/thkx/agentmesh/internal/agent/executor/cli/codex"
	"github.com/thkx/agentmesh/internal/agent/executor/cli/gemini"
	"github.com/thkx/agentmesh/internal/agent/executor/cli/opencode"
	"github.com/thkx/agentmesh/internal/agent/executor/cli/process"
	"github.com/thkx/agentmesh/internal/domain/execution"
)

type Adapter interface {
	Build(execution.Request) (process.Spec, error)
}

func DefaultAdapters(binaries map[execution.Provider]string) map[execution.Provider]Adapter {
	get := func(p execution.Provider, fallback string) string {
		if v := strings.TrimSpace(binaries[p]); v != "" {
			return v
		}
		return fallback
	}
	return map[execution.Provider]Adapter{
		execution.ProviderClaude:   commandAdapter{path: get(execution.ProviderClaude, "claude"), kind: execution.ProviderClaude},
		execution.ProviderCodex:    commandAdapter{path: get(execution.ProviderCodex, "codex"), kind: execution.ProviderCodex},
		execution.ProviderGemini:   commandAdapter{path: get(execution.ProviderGemini, "gemini"), kind: execution.ProviderGemini},
		execution.ProviderOpenCode: commandAdapter{path: get(execution.ProviderOpenCode, "opencode"), kind: execution.ProviderOpenCode},
	}
}

type commandAdapter struct {
	path string
	kind execution.Provider
}

func (a commandAdapter) Build(r execution.Request) (process.Spec, error) {
	if _, err := exec.LookPath(a.path); err != nil {
		return process.Spec{}, fmt.Errorf("%s CLI not found: %w", a.kind, err)
	}
	mode := r.PermissionMode
	if mode == "" {
		mode = execution.PermissionReadOnly
	}
	var args []string
	switch a.kind {
	case execution.ProviderClaude:
		args = claude.Args(r.Prompt, r.Model, mode)
	case execution.ProviderCodex:
		args = codex.Args(r.Prompt, r.Model, r.WorkspacePath, mode)
	case execution.ProviderGemini:
		args = gemini.Args(r.Prompt, r.Model, mode)
	case execution.ProviderOpenCode:
		args = opencode.Args(r.Prompt, r.Model, r.WorkspacePath, mode)
	}
	return process.Spec{Path: a.path, Args: args, Dir: r.WorkspacePath}, nil
}

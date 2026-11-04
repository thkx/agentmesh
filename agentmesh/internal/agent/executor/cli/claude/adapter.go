package claude

import "github.com/thkx/agentmesh/internal/domain/execution"

func Args(prompt, model string, mode execution.PermissionMode) []string {
	permission := "plan"
	if mode == execution.PermissionWorkspaceWrite {
		permission = "acceptEdits"
	}
	a := []string{"-p", prompt, "--output-format", "json", "--permission-mode", permission}
	if model != "" {
		a = append(a, "--model", model)
	}
	return a
}

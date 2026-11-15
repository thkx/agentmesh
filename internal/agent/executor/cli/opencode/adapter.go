package opencode

import "github.com/thkx/agentmesh/internal/domain/execution"

func Args(prompt, model, workspace string, mode execution.PermissionMode) []string {
	a := []string{"run", "--format", "json", "--dir", workspace}
	if mode == execution.PermissionReadOnly {
		a = append(a, "--agent", "plan")
	}
	if model != "" {
		a = append(a, "--model", model)
	}
	return append(a, prompt)
}

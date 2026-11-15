package gemini

import "github.com/thkx/agentmesh/internal/domain/execution"

func Args(prompt, model string, mode execution.PermissionMode) []string {
	approval := "plan"
	if mode == execution.PermissionWorkspaceWrite {
		approval = "auto_edit"
	}
	a := []string{"--prompt", prompt, "--output-format", "json", "--approval-mode", approval}
	if model != "" {
		a = append(a, "--model", model)
	}
	return a
}

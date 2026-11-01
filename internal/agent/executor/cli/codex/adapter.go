package codex

import "github.com/thkx/agentmesh/internal/domain/execution"

func Args(prompt, model, workspace string, mode execution.PermissionMode) []string {
	a := []string{"exec", "--json", "--cd", workspace, "--sandbox", string(mode), "--ask-for-approval", "never"}
	if model != "" {
		a = append(a, "--model", model)
	}
	return append(a, prompt)
}

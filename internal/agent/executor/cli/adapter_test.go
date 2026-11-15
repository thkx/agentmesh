package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/thkx/agentmesh/internal/domain/execution"
)

func TestProviderAdapterBuildsNonInteractiveCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("path fixture")
	}
	bin := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		provider execution.Provider
		want     []string
	}{
		{execution.ProviderClaude, []string{"-p", "do task", "--output-format", "json", "--permission-mode", "plan", "--model", "test-model"}},
		{execution.ProviderCodex, []string{"exec", "--json", "--cd", "/tmp/work", "--sandbox", "read-only", "--ask-for-approval", "never", "--model", "test-model", "do task"}},
		{execution.ProviderGemini, []string{"--prompt", "do task", "--output-format", "json", "--approval-mode", "plan", "--model", "test-model"}},
		{execution.ProviderOpenCode, []string{"run", "--format", "json", "--dir", "/tmp/work", "--agent", "plan", "--model", "test-model", "do task"}},
	}
	for _, tc := range cases {
		t.Run(string(tc.provider), func(t *testing.T) {
			a := commandAdapter{path: bin, kind: tc.provider}
			spec, err := a.Build(execution.Request{Provider: tc.provider, Prompt: "do task", WorkspacePath: "/tmp/work", Model: "test-model", PermissionMode: execution.PermissionReadOnly})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(spec.Args, tc.want) {
				t.Fatalf("args mismatch\n got: %#v\nwant: %#v", spec.Args, tc.want)
			}
		})
	}
}

package capability

import (
	wf "github.com/thkx/agentmesh/internal/domain/workflow"
	"testing"
)

func TestToolSetAppliesAllowlistAndPrompt(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(ToolSet{ID: "read-only", Tools: []string{"read_file", "list_files"}, PromptPrefix: "Use read-only tools."}); err != nil {
		t.Fatal(err)
	}
	cfg := &wf.AgentConfig{Prompt: "Inspect the project", ApprovedTools: []string{"read_file"}}
	if err := r.Apply([]string{"read-only"}, cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.ApprovedTools) != 2 || cfg.ApprovedTools[1] != "list_files" {
		t.Fatalf("unexpected tools: %#v", cfg.ApprovedTools)
	}
	if cfg.Prompt != "Use read-only tools.\n\nInspect the project" {
		t.Fatalf("unexpected prompt: %q", cfg.Prompt)
	}
}

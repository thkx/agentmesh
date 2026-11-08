package agent

import "testing"

func TestDefinitionValidate(t *testing.T) {
	d := Definition{ID: "agent-1", Name: "reviewer", SystemPrompt: "You review code", ModelID: "model-1", Temperature: 0.2, MaxSteps: 10, MaxToolCalls: 4, Enabled: true, ApprovedTools: []string{"file.read"}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	d.Temperature = 2.1
	if err := d.Validate(); err == nil {
		t.Fatal("expected invalid temperature")
	}
	d.Temperature = 0.2
	d.ApprovedTools = []string{"file.read", "file.read"}
	if err := d.Validate(); err == nil {
		t.Fatal("expected duplicate tool rejection")
	}
}

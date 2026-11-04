package agent

import "testing"

func TestTaskValidate(t *testing.T) {
	base := Task{ID: "task-1", ModelID: "model-1", WorkspaceID: "workspace-1", Prompt: "hello"}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	base.TimeoutSeconds = 3601
	if err := base.Validate(); err == nil {
		t.Fatal("expected timeout upper bound validation")
	}
}

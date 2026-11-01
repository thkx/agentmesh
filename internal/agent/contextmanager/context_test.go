package contextmanager

import (
	"github.com/thkx/agentmesh/internal/modelgateway"
	"testing"
)

func TestTrimKeepsSystemAndNewestMessages(t *testing.T) {
	in := []modelgateway.Message{{Role: modelgateway.RoleSystem, Content: "system"}, {Role: modelgateway.RoleUser, Content: "old"}, {Role: modelgateway.RoleAssistant, Content: "new"}}
	got := Trim(in, 10)
	if len(got) != 2 || got[0].Role != modelgateway.RoleSystem || got[1].Content != "new" {
		t.Fatalf("unexpected trim: %+v", got)
	}
}

func TestTrimKeepsAssistantToolCallWithToolResult(t *testing.T) {
	in := []modelgateway.Message{
		{Role: modelgateway.RoleSystem, Content: "sys"},
		{Role: modelgateway.RoleUser, Content: "old user message"},
		{Role: modelgateway.RoleAssistant, Content: `{"type":"tool_call"}`},
		{Role: modelgateway.RoleUser, Name: "tool:file.read", Content: `Tool result for file.read: {"ok":true}`},
	}
	got := Trim(in, len("sys")+len(`{"type":"tool_call"}`)+len("Tool result for file.read: ")+len(`{"ok":true}`))
	if len(got) != 3 || got[1].Role != modelgateway.RoleAssistant || got[2].Role != modelgateway.RoleUser || got[2].Name != "tool:file.read" {
		t.Fatalf("tool call/result pair was not preserved: %+v", got)
	}
}

func TestTrimKeepsNativeToolCallAndMatchingResult(t *testing.T) {
	in := []modelgateway.Message{
		{Role: modelgateway.RoleSystem, Content: "sys"},
		{Role: modelgateway.RoleUser, Content: "old"},
		{Role: modelgateway.RoleAssistant, ToolCalls: []modelgateway.ToolCall{{ID: "call-1", Name: "file.read", Arguments: []byte(`{"path":"README.md"}`)}}},
		{Role: modelgateway.RoleTool, ToolCallID: "call-1", Name: "file.read", Content: `{"ok":true}`},
	}
	budget := len("sys") + len(`{"ok":true}`) + len(`[ {"id":"call-1","name":"file.read","arguments":{"path":"README.md"}} ]`)
	got := Trim(in, budget)
	if len(got) != 3 || len(got[1].ToolCalls) != 1 || got[2].ToolCallID != "call-1" {
		t.Fatalf("native tool call/result pair was not preserved: %+v", got)
	}
}

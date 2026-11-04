package ollamaadapter

import (
	"encoding/json"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"testing"
)

func TestBuildRequestIncludesNativeToolsAndHistory(t *testing.T) {
	req := modelgateway.GenerateRequest{ModelID: "m", ProviderModel: "llama3.1", Messages: []modelgateway.Message{{Role: modelgateway.RoleAssistant, ToolCalls: []modelgateway.ToolCall{{ID: "c1", Name: "file.read", Arguments: json.RawMessage(`{"path":"README.md"}`)}}}, {Role: modelgateway.RoleTool, Content: `{"ok":true}`, ToolCallID: "c1"}}, Tools: []modelgateway.ToolDefinition{{Name: "file.read", Description: "read", Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`)}}}
	got, err := buildRequest(req, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 1 || len(got.Messages) != 2 || len(got.Messages[0].ToolCalls) != 1 {
		t.Fatalf("native tool request not mapped: %+v", got)
	}
}

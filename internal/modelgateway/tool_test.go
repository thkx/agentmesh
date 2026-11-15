package modelgateway

import (
	"encoding/json"
	"testing"
)

func TestToolCallValidate(t *testing.T) {
	good := ToolCall{ID: "call-1", Name: "file.read", Arguments: json.RawMessage(`{"path":"README.md"}`)}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := ToolCall{ID: "call-1", Name: "file.read", Arguments: json.RawMessage(`[]`)}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected object arguments validation error")
	}
}
func TestGenerateRequestAllowsAssistantNativeToolCall(t *testing.T) {
	req := GenerateRequest{ModelID: "m1", Messages: []Message{{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "file.read", Arguments: json.RawMessage(`{}`)}}}}}
	if err := req.Validate(); err != nil {
		t.Fatal(err)
	}
}

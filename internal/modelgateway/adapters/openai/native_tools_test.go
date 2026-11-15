package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thkx/agentmesh/internal/modelgateway"
)

func TestGenerateNativeToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatal("missing bearer credential")
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request["tools"].([]any)) != 1 {
			t.Fatal("expected one native tool definition")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-1","model":"gpt-test","status":"completed","output":[{"type":"function_call","call_id":"call-1","name":"file.read","arguments":"{\"path\":\"README.md\"}"}],"usage":{"input_tokens":10,"output_tokens":3}}`))
	}))
	defer server.Close()
	adapter := &Adapter{apiKey: "test-key", baseURL: server.URL + "/v1", httpClient: server.Client()}
	got, err := adapter.Generate(context.Background(), modelgateway.GenerateRequest{ModelID: "model-1", ProviderModel: "gpt-test", Messages: []modelgateway.Message{{Role: modelgateway.RoleUser, Content: "read"}}, Tools: []modelgateway.ToolDefinition{{Name: "file.read", Parameters: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Name != "file.read" || got.ToolCalls[0].ID != "call-1" {
		t.Fatalf("unexpected tool calls: %+v", got.ToolCalls)
	}
}

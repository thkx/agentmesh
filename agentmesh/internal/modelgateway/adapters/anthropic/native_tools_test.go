package anthropicadapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/thkx/agentmesh/internal/modelgateway"
)

func TestGenerateNativeToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Fatal("missing API key header")
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request["tools"].([]any)) != 1 {
			t.Fatal("expected tool definition")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg-1","model":"claude-test","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu-1","name":"file.read","input":{"path":"README.md"}}],"usage":{"input_tokens":5,"output_tokens":4}}`))
	}))
	defer server.Close()
	adapter := &Adapter{apiKey: "test-key", baseURL: server.URL, httpClient: server.Client()}
	got, err := adapter.Generate(context.Background(), modelgateway.GenerateRequest{ModelID: "m", ProviderModel: "claude-test", Messages: []modelgateway.Message{{Role: modelgateway.RoleUser, Content: "read"}}, Tools: []modelgateway.ToolDefinition{{Name: "file.read", Parameters: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].ID != "toolu-1" {
		t.Fatalf("unexpected calls: %+v", got.ToolCalls)
	}
}

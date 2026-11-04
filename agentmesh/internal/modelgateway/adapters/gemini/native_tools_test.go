package geminiadapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/thkx/agentmesh/internal/modelgateway"
)

func TestGenerateNativeFunctionCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/v1beta/models/gemini-test:generateContent") {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("key") != "test-key" {
			t.Fatal("missing API key query parameter")
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request["tools"].([]any)) != 1 {
			t.Fatal("expected function declaration")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"file.read","args":{"path":"README.md"}}}]}}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3}}`))
	}))
	defer server.Close()
	adapter := &Adapter{apiKey: "test-key", baseURL: server.URL, httpClient: server.Client()}
	got, err := adapter.Generate(context.Background(), modelgateway.GenerateRequest{ModelID: "m", ProviderModel: "gemini-test", Messages: []modelgateway.Message{{Role: modelgateway.RoleUser, Content: "read"}}, Tools: []modelgateway.ToolDefinition{{Name: "file.read", Parameters: json.RawMessage(`{"type":"object"}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Name != "file.read" {
		t.Fatalf("unexpected calls: %+v", got.ToolCalls)
	}
}

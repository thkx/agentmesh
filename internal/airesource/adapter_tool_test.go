package airesource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGeneratePreservesNativeToolCalling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Errorf("unexpected authorization header")
		}
		var payload struct {
			Model    string          `json:"model"`
			Messages []map[string]any `json:"messages"`
			Tools    []Tool          `json:"tools"`
			Choice   string          `json:"tool_choice"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.Model != "glm-5.3-flash" || len(payload.Tools) != 1 || payload.Choice != "auto" {
			t.Errorf("unexpected tool request payload: %+v", payload)
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","created":1,"model":"glm-5.3-flash","choices":[{"index":0,"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"hello\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	defer server.Close()

	candidate := Candidate{
		Provider: Provider{Protocol: ProtocolOpenAI},
		Endpoint: Endpoint{BaseURL: server.URL + "/v1", TimeoutMS: 2000},
		UpstreamModel: "glm-5.3-flash",
		Secret: "test-secret",
	}
	req := GenerateRequest{
		Model: "glm-5.3-flash",
		Messages: []Message{{Role: "user", Content: "hello"}},
		Tools: []Tool{{Type: "function", Function: ToolFunction{
			Name: "lookup", Parameters: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		}}},
		ToolChoice: "auto",
	}
	resp, status, err := generate(context.Background(), candidate, req)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if status != http.StatusOK || len(resp.Choices) != 1 || len(resp.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("unexpected tool response: status=%d response=%+v", status, resp)
	}
	call := resp.Choices[0].Message.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "lookup" || call.Function.Arguments != `{"q":"hello"}` {
		t.Fatalf("unexpected tool call: %+v", call)
	}
}

func TestNormalizeMessagesPreservesAssistantToolCallAndToolResult(t *testing.T) {
	messages, system := normalizeMessages([]Message{
		{Role: "system", Content: "system prompt"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Type: "function", Function: ToolCallFunction{Name: "lookup", Arguments: `{"q":"x"}`}}}},
		{Role: "tool", Name: "lookup", ToolCallID: "call_1", Content: "result"},
	})
	if system != "system prompt" || len(messages) != 2 {
		t.Fatalf("unexpected normalized messages: system=%q messages=%+v", system, messages)
	}
	if messages[0]["role"] != "assistant" || messages[1]["role"] != "tool" || messages[1]["tool_call_id"] != "call_1" {
		t.Fatalf("tool conversation was not preserved: %+v", messages)
	}
	_ = time.Second
}

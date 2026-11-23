package airesource

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thkx/agentmesh/internal/config"
	"github.com/thkx/agentmesh/internal/platform/cryptography"
	"github.com/thkx/agentmesh/internal/platform/database"
	"github.com/thkx/agentmesh/internal/modelgateway"
)

func newRouterTestEnvironment(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	dbConfig := config.Defaults().Database
	dbConfig.DSN = filepath.Join(t.TempDir(), "airesource-router.db")
	sqliteDB, err := database.OpenSQLite(context.Background(), dbConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqliteDB.Close() })
	if err = database.RunMigrations(sqliteDB.DB); err != nil {
		t.Fatal(err)
	}
	cipher, err := cryptography.NewAESGCM([]byte(strings.Repeat("r", 32)))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(sqliteDB.DB, cipher)
	if err != nil {
		t.Fatal(err)
	}
	return service, sqliteDB.DB
}

func addRouterTestModel(t *testing.T, service *Service, modelID, modelKey string, endpoints []struct {
	id           string
	url          string
	priority     int
	credentialID string
	key          string
}) {
	t.Helper()
	ctx := context.Background()
	if err := service.CreateProvider(ctx, Provider{
		ID: "provider-" + modelID, Name: "Router test provider",
		Protocol: ProtocolOpenAI, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.CreateModel(ctx, Model{
		ID: modelID, ModelKey: modelKey, DisplayName: modelKey,
		Enabled: true, SupportsStreaming: true,
	}); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range endpoints {
		if err := service.CreateEndpoint(ctx, Endpoint{
			ID: endpoint.id, ProviderID: "provider-" + modelID,
			Name: endpoint.id, BaseURL: endpoint.url,
			Priority: endpoint.priority, TimeoutMS: 5000, Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
		if err := service.CreateCredential(ctx, CredentialInput{
			ID: endpoint.credentialID, ProviderID: "provider-" + modelID,
			EndpointID: endpoint.id, Name: endpoint.credentialID,
			APIKey: endpoint.key, Priority: endpoint.priority,
			Weight: 1, MaxConcurrency: 2,
		}); err != nil {
			t.Fatal(err)
		}
		if err := service.BindModel(ctx, BindingInput{
			CredentialID: endpoint.credentialID, ModelID: modelID,
			UpstreamModel: "upstream-" + modelKey,
		}); err != nil {
			t.Fatal(err)
		}
	}
}


func TestModelGatewayRouterForwardsJSONResponseFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var format map[string]string
		if err := json.Unmarshal(payload["response_format"], &format); err != nil {
			t.Errorf("decode response format: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if format["type"] != "json_object" {
			t.Errorf("response_format.type = %q, want json_object", format["type"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"json-mode-response","object":"chat.completion","created":1,"model":"upstream","choices":[{"index":0,"message":{"role":"assistant","content":"{\\"ok\\":true}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":3,"total_tokens":7}}`))
	}))
	defer server.Close()

	service, _ := newRouterTestEnvironment(t)
	addRouterTestModel(t, service, "json-model", "json-model-key", []struct {
		id           string
		url          string
		priority     int
		credentialID string
		key          string
	}{{
		id: "json-endpoint", url: server.URL + "/v1", priority: 1,
		credentialID: "json-credential", key: "json-secret",
	}})
	router, err := NewRouter(service)
	if err != nil {
		t.Fatal(err)
	}
	bridge := NewModelGatewayRouter(service, router)
	response, err := bridge.Generate(context.Background(), modelgateway.GenerateRequest{
		ModelID: "json-model", ProviderModel: "json-model-key",
		Messages: []modelgateway.Message{{Role: modelgateway.RoleUser, Content: "JSON"}},
		ResponseFormat: &modelgateway.ResponseFormat{Type: "json_object"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Content != `{"ok":true}` {
		t.Fatalf("content = %q", response.Content)
	}
}

func TestRouterFailsOverAndPersistsHealthAndUsage(t *testing.T) {
	var firstCalls atomic.Int32
	var secondCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected first endpoint path %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream echoed api-secret-first"))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer api-secret-second" {
			t.Errorf("second credential was not selected: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-success", "object": "chat.completion", "created": time.Now().Unix(),
			"model": "upstream-router-model",
			"choices": []any{map[string]any{
				"index": 0, "message": map[string]any{"role": "assistant", "content": "failover-ok"},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10},
		})
	}))
	defer second.Close()

	service, db := newRouterTestEnvironment(t)
	addRouterTestModel(t, service, "router-model", "router-model-key", []struct {
		id           string
		url          string
		priority     int
		credentialID string
		key          string
	}{
		{id: "endpoint-first", url: first.URL + "/v1", priority: 1, credentialID: "credential-first", key: "api-secret-first"},
		{id: "endpoint-second", url: second.URL + "/v1", priority: 2, credentialID: "credential-second", key: "api-secret-second"},
	})
	router, err := NewRouter(service)
	if err != nil {
		t.Fatal(err)
	}
	response, err := router.Generate(context.Background(), GenerateRequest{
		Model: "router-model-key", Messages: []Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 || response.Choices[0].Message.Content != "failover-ok" {
		t.Fatalf("unexpected failover response: %+v", response)
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("expected one call per credential, got first=%d second=%d", firstCalls.Load(), secondCalls.Load())
	}
	var failures, successes, usages int
	if err = db.QueryRow("SELECT COUNT(*) FROM ai_request_logs WHERE status='failure' AND credential_id='credential-first'").Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM ai_request_logs WHERE status='success' AND credential_id='credential-second'").Scan(&successes); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM ai_usage_records").Scan(&usages); err != nil {
		t.Fatal(err)
	}
	if failures != 1 || successes != 1 || usages != 1 {
		t.Fatalf("request accounting mismatch: failures=%d successes=%d usages=%d", failures, successes, usages)
	}
	var firstHealth, secondHealth string
	if err = db.QueryRow("SELECT state FROM ai_credential_health WHERE credential_id='credential-first'").Scan(&firstHealth); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT state FROM ai_credential_health WHERE credential_id='credential-second'").Scan(&secondHealth); err != nil {
		t.Fatal(err)
	}
	if firstHealth != "degraded" || secondHealth != "healthy" {
		t.Fatalf("unexpected credential health: first=%q second=%q", firstHealth, secondHealth)
	}
}

func TestRouterStreamsOpenAICompatibleSSEAndRecordsUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected stream path %q", r.URL.Path)
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Errorf("missing SSE Accept header: %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"stream-1\",\"model\":\"upstream-stream-model\",\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"world\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}))
	defer server.Close()

	service, db := newRouterTestEnvironment(t)
	addRouterTestModel(t, service, "stream-model", "stream-model-key", []struct {
		id           string
		url          string
		priority     int
		credentialID string
		key          string
	}{
		{id: "stream-endpoint", url: server.URL + "/v1", priority: 1, credentialID: "stream-credential", key: "stream-secret"},
	})
	router, err := NewRouter(service)
	if err != nil {
		t.Fatal(err)
	}
	events, err := router.Stream(context.Background(), GenerateRequest{
		Model: "stream-model-key", Messages: []Message{{Role: "user", Content: "stream"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var completed bool
	var gotUsage bool
	for event := range events {
		switch event.Type {
		case "text_delta":
			text.WriteString(event.Delta)
		case "usage":
			gotUsage = event.Usage.TotalTokens == 6
		case "completed":
			completed = event.Response != nil && len(event.Response.Choices) == 1
		case "error":
			t.Fatalf("unexpected stream error: %v", event.Err)
		}
	}
	if text.String() != "hello world" || !completed || !gotUsage {
		t.Fatalf("incomplete SSE stream: text=%q completed=%v usage=%v", text.String(), completed, gotUsage)
	}
	var logs, usages int
	if err = db.QueryRow("SELECT COUNT(*) FROM ai_request_logs WHERE status='success' AND credential_id='stream-credential'").Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT COUNT(*) FROM ai_usage_records WHERE total_tokens=6").Scan(&usages); err != nil {
		t.Fatal(err)
	}
	if logs != 1 || usages != 1 {
		t.Fatalf("stream accounting mismatch: logs=%d usages=%d", logs, usages)
	}
}

func TestRouterFailsOverOnUnauthorizedRateLimitAndServerErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var firstCalls atomic.Int32
			var secondCalls atomic.Int32
			first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				firstCalls.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte("upstream failure"))
			}))
			defer first.Close()
			second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				secondCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"recovered","object":"chat.completion","created":1,"model":"upstream","choices":[{"index":0,"message":{"role":"assistant","content":"recovered"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
			}))
			defer second.Close()

			service, db := newRouterTestEnvironment(t)
			modelID := "retry-model-" + strings.ReplaceAll(http.StatusText(status), " ", "-")
			modelKey := modelID + "-key"
			addRouterTestModel(t, service, modelID, modelKey, []struct {
				id           string
				url          string
				priority     int
				credentialID string
				key          string
			}{
				{id: "retry-first-" + modelID, url: first.URL + "/v1", priority: 1, credentialID: "retry-credential-first-" + modelID, key: "first-secret"},
				{id: "retry-second-" + modelID, url: second.URL + "/v1", priority: 2, credentialID: "retry-credential-second-" + modelID, key: "second-secret"},
			})
			router, err := NewRouter(service)
			if err != nil {
				t.Fatal(err)
			}
			response, err := router.Generate(context.Background(), GenerateRequest{
				Model: modelKey, Messages: []Message{{Role: "user", Content: "retry"}},
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(response.Choices) != 1 || response.Choices[0].Message.Content != "recovered" {
				t.Fatalf("unexpected recovered response: %+v", response)
			}
			if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
				t.Fatalf("expected failover after HTTP %d, calls first=%d second=%d", status, firstCalls.Load(), secondCalls.Load())
			}
			var failedStatus int
			if err = db.QueryRow("SELECT status_code FROM ai_request_logs WHERE credential_id=?", "retry-credential-first-"+modelID).Scan(&failedStatus); err != nil {
				t.Fatal(err)
			}
			if failedStatus != status {
				t.Fatalf("failure status was not persisted: got %d want %d", failedStatus, status)
			}
		})
	}
}

func TestRouterFailsOverAfterEndpointTimeout(t *testing.T) {
	var firstCalls atomic.Int32
	var secondCalls atomic.Int32
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCalls.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		secondCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"timeout-recovered","object":"chat.completion","created":1,"model":"upstream","choices":[{"index":0,"message":{"role":"assistant","content":"timeout-recovered"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
	}))
	defer second.Close()

	service, db := newRouterTestEnvironment(t)
	addRouterTestModel(t, service, "timeout-model", "timeout-model-key", []struct {
		id           string
		url          string
		priority     int
		credentialID string
		key          string
	}{
		{id: "timeout-first", url: first.URL + "/v1", priority: 1, credentialID: "timeout-credential-first", key: "first-secret"},
		{id: "timeout-second", url: second.URL + "/v1", priority: 2, credentialID: "timeout-credential-second", key: "second-secret"},
	})
	if _, err := db.Exec("UPDATE ai_endpoints SET timeout_ms=25 WHERE id='timeout-first'"); err != nil {
		t.Fatal(err)
	}
	router, err := NewRouter(service)
	if err != nil {
		t.Fatal(err)
	}
	requestCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, err := router.Generate(requestCtx, GenerateRequest{
		Model: "timeout-model-key", Messages: []Message{{Role: "user", Content: "timeout"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Choices) != 1 || response.Choices[0].Message.Content != "timeout-recovered" {
		t.Fatalf("unexpected timeout recovery response: %+v", response)
	}
	if firstCalls.Load() != 1 || secondCalls.Load() != 1 {
		t.Fatalf("expected timeout failover, calls first=%d second=%d", firstCalls.Load(), secondCalls.Load())
	}
	var failures, statusCode int
	if err = db.QueryRow("SELECT COUNT(*),COALESCE(MAX(status_code),-1) FROM ai_request_logs WHERE credential_id='timeout-credential-first' AND status='failure'").Scan(&failures, &statusCode); err != nil {
		t.Fatal(err)
	}
	if failures != 1 || statusCode != 0 {
		t.Fatalf("timed out attempt was not recorded as a network timeout: failures=%d status_code=%d", failures, statusCode)
	}
	var diagnosticCode string
	if err = db.QueryRow("SELECT error_code FROM ai_request_logs WHERE credential_id='timeout-credential-first' AND status='failure'").Scan(&diagnosticCode); err != nil {
		t.Fatal(err)
	}
	if diagnosticCode != "request_timeout" {
		t.Fatalf("timed out request diagnostic = %q, want request_timeout", diagnosticCode)
	}
}

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
		id string
		url string
		priority     int
		credentialID string
		key string
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

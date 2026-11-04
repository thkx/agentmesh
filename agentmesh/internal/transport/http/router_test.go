package http_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	_ "modernc.org/sqlite"

	domaincredential "github.com/thkx/agentmesh/internal/domain/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
	ollamaadapter "github.com/thkx/agentmesh/internal/modelgateway/adapters/ollama"
	"github.com/thkx/agentmesh/internal/platform/database"
	httptransport "github.com/thkx/agentmesh/internal/transport/http"
)

func TestCatalogHTTPProviderModelLifecycle(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(t.TempDir(), "api.db"))+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if err := database.RunMigrations(sqlDB); err != nil {
		t.Fatal(err)
	}
	repo, err := database.NewRuntimeRepository(sqlDB)
	if err != nil {
		t.Fatal(err)
	}

	providers := provider.NewProviderRegistry()
	models := provider.NewModelRegistry()
	adapters := modelgateway.NewRegistry()
	factory := modelgateway.NewAdapterFactory()
	if err := factory.Register(provider.TypeOllama, func(p provider.Provider) (modelgateway.Model, error) { return ollamaadapter.New(p.BaseURL, nil) }); err != nil {
		t.Fatal(err)
	}
	runtime, err := modelgateway.NewProviderRuntime(providers, adapters, factory)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := modelgateway.NewGatewayWithProviders(adapters, models, providers)
	if err != nil {
		t.Fatal(err)
	}
	router := httptransport.NewRouter(httptransport.Dependencies{DB: sqlDB, Providers: providers, Models: models, Adapters: adapters, ProviderRuntime: runtime, Repository: repo, Gateway: gateway})

	do := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)
		return rr
	}

	rr := do(http.MethodPost, "/api/v1/providers", `{"id":"ollama-local","name":"Local Ollama","type":"ollama","kind":"local","base_url":"http://127.0.0.1:11434","enabled":true}`)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create provider: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("api_key_env")) {
		t.Fatalf("credential reference leaked: %s", rr.Body.String())
	}

	// Concurrent catalog writes must not overwrite one another's persisted snapshots.
	type createResult struct {
		code int
		body string
	}
	results := make(chan createResult, 2)
	var wg sync.WaitGroup
	for _, payload := range []string{
		`{"id":"llama-local","provider_id":"ollama-local","name":"llama3.1","supports_streaming":true,"enabled":true}`,
		`{"id":"llama-backup","provider_id":"ollama-local","name":"llama3.2","supports_streaming":true,"enabled":true}`,
	} {
		wg.Add(1)
		go func(body string) {
			defer wg.Done()
			created := do(http.MethodPost, "/api/v1/models", body)
			results <- createResult{code: created.Code, body: created.Body.String()}
		}(payload)
	}
	wg.Wait()
	close(results)
	for result := range results {
		if result.code != http.StatusCreated {
			t.Fatalf("concurrent model create: status=%d body=%s", result.code, result.body)
		}
	}
	listed := do(http.MethodGet, "/api/v1/models", "")
	if listed.Code != http.StatusOK || !bytes.Contains(listed.Body.Bytes(), []byte("llama-local")) || !bytes.Contains(listed.Body.Bytes(), []byte("llama-backup")) {
		t.Fatalf("concurrent catalog writes lost a model: status=%d body=%s", listed.Code, listed.Body.String())
	}

	rr = do(http.MethodPost, "/api/v1/providers/ollama-local/test", "")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("provider upstream test should fail without a listening Ollama server: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if bytes.Contains(rr.Body.Bytes(), []byte("127.0.0.1:11434")) {
		t.Fatalf("raw upstream error leaked: %s", rr.Body.String())
	}

	rr = do(http.MethodDelete, "/api/v1/providers/ollama-local", "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("delete referenced provider should conflict: status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = do(http.MethodDelete, "/api/v1/models/llama-local", "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete model: status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = do(http.MethodDelete, "/api/v1/models/llama-backup", "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete second model: status=%d body=%s", rr.Code, rr.Body.String())
	}
	rr = do(http.MethodDelete, "/api/v1/providers/ollama-local", "")
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete provider: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHealthEndpoints(t *testing.T) {
	router := httptransport.NewRouter(httptransport.Dependencies{})
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("healthz status=%d body=%s", rr.Code, rr.Body.String())
	}
}

type httpTestAdapter struct{}

func (httpTestAdapter) Generate(_ context.Context, req modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	return &modelgateway.GenerateResponse{Content: "generated:" + req.ProviderModel}, nil
}
func (httpTestAdapter) Stream(_ context.Context, req modelgateway.GenerateRequest) (<-chan modelgateway.StreamEvent, error) {
	ch := make(chan modelgateway.StreamEvent, 2)
	ch <- modelgateway.NewTextDeltaEvent(req.ProviderID, req.ProviderModel, "hello")
	ch <- modelgateway.NewCompletedEvent(&modelgateway.GenerateResponse{Content: "hello"})
	close(ch)
	return ch, nil
}

func TestGenerateAndSSEHTTPReachGateway(t *testing.T) {
	providers := provider.NewProviderRegistry()
	p := provider.Provider{ID: "ollama-local", Type: provider.TypeOllama, Name: "Local", Kind: provider.KindLocal, BaseURL: "http://127.0.0.1:11434", Enabled: true}
	if err := providers.Register(p); err != nil {
		t.Fatal(err)
	}
	models := provider.NewModelRegistry()
	m := provider.Model{ID: "llama-local", ProviderID: p.ID, Name: "llama3.1", MaxOutputTokens: 1024, SupportsStreaming: true, Enabled: true}
	if err := models.Register(m); err != nil {
		t.Fatal(err)
	}
	adapters := modelgateway.NewRegistry()
	if err := adapters.Register(p.ID, httpTestAdapter{}); err != nil {
		t.Fatal(err)
	}
	gateway, err := modelgateway.NewGatewayWithProviders(adapters, models, providers)
	if err != nil {
		t.Fatal(err)
	}
	router := httptransport.NewRouter(httptransport.Dependencies{Providers: providers, Models: models, Adapters: adapters, Gateway: gateway})
	body := `{"model_id":"llama-local","messages":[{"role":"user","content":"hello"}],"max_output_tokens":128}`

	generate := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/generate", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(generate, req)
	if generate.Code != http.StatusOK {
		t.Fatalf("generate status=%d body=%s", generate.Code, generate.Body.String())
	}
	var response modelgateway.GenerateResponse
	if err := json.Unmarshal(generate.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.ProviderID != p.ID || response.ModelID != m.ID || response.Content != "generated:llama3.1" {
		t.Fatalf("unexpected normalized response: %+v", response)
	}

	stream := httptest.NewRecorder()
	streamReq := httptest.NewRequest(http.MethodPost, "/api/v1/generate/stream", bytes.NewBufferString(body))
	streamReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(stream, streamReq)
	if stream.Code != http.StatusOK {
		t.Fatalf("stream status=%d body=%s", stream.Code, stream.Body.String())
	}
	if got := stream.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("unexpected stream content type %q", got)
	}
	for _, event := range []string{"event: text_delta", `"provider_id":"ollama-local"`, `"model_id":"llama-local"`, "event: completed"} {
		if !strings.Contains(stream.Body.String(), event) {
			t.Fatalf("SSE response missing %q: %s", event, stream.Body.String())
		}
	}
}

type httpCredentialManager struct {
	record domaincredential.Record
	secret string
}

func (m *httpCredentialManager) Set(_ context.Context, _ provider.ID, secret domaincredential.Secret) error {
	m.secret = secret.Reveal()
	m.record.Status = domaincredential.StatusConfigured
	m.record.Source = domaincredential.SourceStored
	return nil
}
func (m *httpCredentialManager) Delete(context.Context, provider.ID) error {
	m.secret = ""
	m.record.Status = domaincredential.StatusUnconfigured
	m.record.Source = ""
	return nil
}
func (m *httpCredentialManager) Status(context.Context, provider.ID) (domaincredential.Record, error) {
	return m.record, nil
}

func TestCredentialHTTPNeverReturnsSecret(t *testing.T) {
	manager := &httpCredentialManager{record: domaincredential.Record{ProviderID: "openai-primary", Status: domaincredential.StatusUnconfigured}}
	router := httptransport.NewRouter(httptransport.Dependencies{CredentialManager: manager})
	put := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/providers/openai-primary/credential", bytes.NewBufferString(`{"api_key":"top-secret-value"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(put, request)
	if put.Code != http.StatusOK {
		t.Fatalf("put status=%d body=%s", put.Code, put.Body.String())
	}
	if bytes.Contains(put.Body.Bytes(), []byte("top-secret-value")) {
		t.Fatal("API response exposed credential plaintext")
	}
	if manager.secret != "top-secret-value" {
		t.Fatal("manager did not receive credential")
	}
	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/providers/openai-primary/credential", nil))
	if get.Code != http.StatusOK || bytes.Contains(get.Body.Bytes(), []byte("top-secret-value")) {
		t.Fatalf("unsafe status response: %d %s", get.Code, get.Body.String())
	}
	del := httptest.NewRecorder()
	router.ServeHTTP(del, httptest.NewRequest(http.MethodDelete, "/api/v1/providers/openai-primary/credential", nil))
	if del.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", del.Code, del.Body.String())
	}
}

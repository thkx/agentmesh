package http_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"github.com/thkx/agentmesh/internal/platform/database"
	httptransport "github.com/thkx/agentmesh/internal/transport/http"
	"github.com/thkx/agentmesh/internal/usage"
)

type p5UsageAdapter struct{}

func (p5UsageAdapter) Generate(_ context.Context, req modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	return &modelgateway.GenerateResponse{
		ID: "provider-request-p5-1",
		Content: "safe test completion",
		Usage: modelgateway.Usage{InputTokens: 11, OutputTokens: 7},
	}, nil
}
func (p5UsageAdapter) Stream(context.Context, modelgateway.GenerateRequest) (<-chan modelgateway.StreamEvent, error) {
	ch := make(chan modelgateway.StreamEvent)
	close(ch)
	return ch, nil
}

func TestP5GenerateHTTPPersistsUsageAndRequestCorrelationOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p5-usage.db")
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := database.RunMigrations(db); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	providers := provider.NewProviderRegistry()
	p := provider.Provider{ID: "test-provider", Type: provider.TypeOpenAI, Name: "P5 test provider", Kind: provider.KindCloud, Enabled: true}
	if err := providers.Register(p); err != nil {
		t.Fatal(err)
	}
	models := provider.NewModelRegistry()
	m := provider.Model{ID: "p5-test-model", ProviderID: p.ID, Name: "upstream-test-model", MaxOutputTokens: 128, Enabled: true}
	if err := models.Register(m); err != nil {
		t.Fatal(err)
	}
	adapters := modelgateway.NewRegistry()
	if err := adapters.Register(p.ID, p5UsageAdapter{}); err != nil {
		t.Fatal(err)
	}
	gateway, err := modelgateway.NewGatewayWithProviders(adapters, models, providers)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := usage.NewLedger(db)
	if err != nil {
		t.Fatal(err)
	}
	gateway.SetUsageObserver(usage.GatewayObserver{Ledger: ledger})

	router := httptransport.NewRouter(httptransport.Dependencies{
		DB: db, Providers: providers, Models: models, Adapters: adapters, Gateway: gateway,
	})
	body := []byte(`{"model_id":"p5-test-model","messages":[{"role":"user","content":"do not persist this prompt"}],"max_output_tokens":64}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/generate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", "p5-request-001")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("generate status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response modelgateway.GenerateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Content != "safe test completion" || response.ProviderID != p.ID || response.ModelID != m.ID {
		t.Fatalf("unexpected generated response: %+v", response)
	}

	var count int
	var requestID, providerID, modelID, source string
	var inputTokens, outputTokens, totalTokens int64
	err = db.QueryRow(`SELECT COUNT(*),MAX(request_id),MAX(provider_id),MAX(model_id),MAX(usage_source),MAX(input_tokens),MAX(output_tokens),MAX(total_tokens)
		FROM model_usage_events WHERE idempotency_key=?`, "provider-request-p5-1").Scan(
		&count, &requestID, &providerID, &modelID, &source, &inputTokens, &outputTokens, &totalTokens,
	)
	if err != nil {
		t.Fatalf("read usage ledger: %v", err)
	}
	if count != 1 {
		t.Fatalf("usage records=%d want=1", count)
	}
	if requestID != "p5-request-001" || providerID != string(p.ID) || modelID != m.ID {
		t.Fatalf("usage provenance mismatch: request=%q provider=%q model=%q", requestID, providerID, modelID)
	}
	if source != "provider_reported" || inputTokens != 11 || outputTokens != 7 || totalTokens != 18 {
		t.Fatalf("usage dimensions mismatch: source=%q input=%d output=%d total=%d", source, inputTokens, outputTokens, totalTokens)
	}

	var duplicateCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM model_usage_events WHERE idempotency_key=?", "provider-request-p5-1").Scan(&duplicateCount); err != nil {
		t.Fatal(err)
	}
	if duplicateCount != 1 {
		t.Fatalf("duplicate usage write detected: %d", duplicateCount)
	}
}

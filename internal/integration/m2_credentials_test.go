package integration_test

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	appcredential "github.com/thkx/agentmesh/internal/credential"
	"github.com/thkx/agentmesh/internal/domain/credential"
	"github.com/thkx/agentmesh/internal/domain/provider"
	"github.com/thkx/agentmesh/internal/modelgateway"
	"github.com/thkx/agentmesh/internal/platform/credentialstore"
	"github.com/thkx/agentmesh/internal/platform/database"
	httptransport "github.com/thkx/agentmesh/internal/transport/http"
)

type integrationKeyProvider struct{}

func (integrationKeyProvider) Key() ([]byte, error) { return bytes.Repeat([]byte{0x42}, 32), nil }

type integrationEnv map[string]string

func (e integrationEnv) Lookup(k string) (string, bool) { v, ok := e[k]; return v, ok }

type credentialTestAdapter struct{ secret string }

func (a *credentialTestAdapter) Generate(context.Context, modelgateway.GenerateRequest) (*modelgateway.GenerateResponse, error) {
	return &modelgateway.GenerateResponse{Content: "ok"}, nil
}
func (a *credentialTestAdapter) Stream(context.Context, modelgateway.GenerateRequest) (<-chan modelgateway.StreamEvent, error) {
	ch := make(chan modelgateway.StreamEvent)
	close(ch)
	return ch, nil
}

func TestCredentialHTTPPersistsEncryptedCredentialAndHotReloadsAdapter(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "m2-credentials.db")
	sqlDB, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?_pragma=foreign_keys(1)")
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
	p := provider.Provider{ID: "openai-primary", Type: provider.TypeOpenAI, Name: "Primary", Kind: provider.KindCloud, CredentialRef: "OPENAI_PRIMARY_KEY", Enabled: true}
	if err := repo.Sync(context.Background(), []provider.Provider{p}, nil); err != nil {
		t.Fatal(err)
	}
	store, err := credentialstore.NewSQLiteStore(sqlDB, integrationKeyProvider{})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := appcredential.NewResolver(store, integrationEnv{})
	if err != nil {
		t.Fatal(err)
	}
	providers := provider.NewProviderRegistry()
	if err := providers.Register(p); err != nil {
		t.Fatal(err)
	}
	models := provider.NewModelRegistry()
	adapters := modelgateway.NewRegistry()
	factory := modelgateway.NewAdapterFactory()
	if err := factory.Register(provider.TypeOpenAI, func(p provider.Provider) (modelgateway.Model, error) {
		resolved, err := resolver.Resolve(context.Background(), p)
		if err != nil {
			return nil, err
		}
		return &credentialTestAdapter{secret: resolved.Secret.Reveal()}, nil
	}); err != nil {
		t.Fatal(err)
	}
	runtime, err := modelgateway.NewProviderRuntime(providers, adapters, factory)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.ApplyProvider(p); err != nil {
		t.Fatal(err)
	}
	if adapters.Has(p.ID) {
		t.Fatal("provider without credentials should not have an adapter")
	}
	manager, err := appcredential.NewManager(store, resolver, providers, runtime)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := modelgateway.NewGatewayWithProviders(adapters, models, providers)
	if err != nil {
		t.Fatal(err)
	}
	router := httptransport.NewRouter(httptransport.Dependencies{DB: sqlDB, Providers: providers, Models: models, Adapters: adapters, ProviderRuntime: runtime, Repository: repo, Gateway: gateway, CredentialManager: manager})
	secret := "integration-secret-not-for-response"
	put := httptest.NewRecorder()
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/providers/openai-primary/credential", bytes.NewBufferString(`{"api_key":"`+secret+`"}`))
	putReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(put, putReq)
	if put.Code != http.StatusOK {
		t.Fatalf("credential PUT: %d %s", put.Code, put.Body.String())
	}
	if bytes.Contains(put.Body.Bytes(), []byte(secret)) {
		t.Fatal("PUT response leaked secret")
	}
	adapterValue, err := adapters.Get(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	adapter, ok := adapterValue.(*credentialTestAdapter)
	if !ok || adapter.secret != secret {
		t.Fatalf("adapter was not hot-reloaded with stored credential: %#v", adapterValue)
	}
	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/api/v1/providers/openai-primary/credential", nil))
	if get.Code != http.StatusOK || bytes.Contains(get.Body.Bytes(), []byte(secret)) {
		t.Fatalf("unsafe credential GET: %d %s", get.Code, get.Body.String())
	}
	var encrypted []byte
	if err := sqlDB.QueryRow(`SELECT encrypted_value FROM provider_credentials WHERE provider_id = ?`, string(p.ID)).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, []byte(secret)) {
		t.Fatal("plaintext credential found in SQLite blob")
	}
	del := httptest.NewRecorder()
	router.ServeHTTP(del, httptest.NewRequest(http.MethodDelete, "/api/v1/providers/openai-primary/credential", nil))
	if del.Code != http.StatusNoContent {
		t.Fatalf("credential DELETE: %d %s", del.Code, del.Body.String())
	}
	if adapters.Has(p.ID) {
		t.Fatal("adapter remained after stored credential deletion")
	}
	if _, err := store.Get(context.Background(), p.ID); err == nil {
		t.Fatal("credential remained in persistent store")
	} else if err != credential.ErrNotFound {
		t.Fatalf("unexpected store error: %v", err)
	}
}

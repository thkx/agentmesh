package airesource

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/thkx/agentmesh/internal/platform/cryptography"
)

func TestImportManifestJSONEncryptsSecretsAndIsIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`PRAGMA foreign_keys=ON`,
		`CREATE TABLE ai_providers(id TEXT PRIMARY KEY,name TEXT NOT NULL,protocol TEXT NOT NULL,enabled INTEGER NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL)`,
		`CREATE TABLE ai_endpoints(id TEXT PRIMARY KEY,provider_id TEXT NOT NULL,name TEXT NOT NULL,base_url TEXT NOT NULL,priority INTEGER NOT NULL,timeout_ms INTEGER NOT NULL,enabled INTEGER NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,UNIQUE(id,provider_id),FOREIGN KEY(provider_id) REFERENCES ai_providers(id))`,
		`CREATE TABLE ai_credentials(id TEXT PRIMARY KEY,provider_id TEXT NOT NULL,endpoint_id TEXT NOT NULL,name TEXT NOT NULL,secret_ciphertext BLOB NOT NULL,status TEXT NOT NULL,priority INTEGER NOT NULL,weight INTEGER NOT NULL,max_concurrency INTEGER NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,FOREIGN KEY(provider_id) REFERENCES ai_providers(id),FOREIGN KEY(endpoint_id,provider_id) REFERENCES ai_endpoints(id,provider_id))`,
		`CREATE TABLE ai_credential_health(credential_id TEXT PRIMARY KEY,updated_at INTEGER NOT NULL,FOREIGN KEY(credential_id) REFERENCES ai_credentials(id))`,
		`CREATE TABLE ai_models(id TEXT PRIMARY KEY,model_key TEXT NOT NULL UNIQUE,display_name TEXT NOT NULL,enabled INTEGER NOT NULL,supports_streaming INTEGER NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL)`,
		`CREATE TABLE ai_credential_models(credential_id TEXT NOT NULL,model_id TEXT NOT NULL,upstream_model TEXT NOT NULL,enabled INTEGER NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL,PRIMARY KEY(credential_id,model_id),FOREIGN KEY(credential_id) REFERENCES ai_credentials(id),FOREIGN KEY(model_id) REFERENCES ai_models(id))`,
		`CREATE TABLE ai_routing_policies(
			model_id TEXT PRIMARY KEY NOT NULL,
			strategy TEXT NOT NULL DEFAULT 'weighted_round_robin'
				CHECK (strategy IN ('priority','round_robin','weighted_round_robin','least_connections')),
			max_retries INTEGER NOT NULL DEFAULT 2 CHECK (max_retries BETWEEN 0 AND 5),
			failure_threshold INTEGER NOT NULL DEFAULT 5 CHECK (failure_threshold > 0),
			cooldown_seconds INTEGER NOT NULL DEFAULT 30 CHECK (cooldown_seconds > 0),
			updated_at INTEGER NOT NULL,
			FOREIGN KEY(model_id) REFERENCES ai_models(id)
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	cipher, err := cryptography.NewAESGCM([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	manifest := ImportManifest{Provider: Provider{ID: "p", Name: "Provider", Protocol: ProtocolOpenAI, Enabled: true}, Endpoint: Endpoint{ID: "e", ProviderID: "p", Name: "Endpoint", BaseURL: "https://example.test/v1", Priority: 1, TimeoutMS: 60000, Enabled: true}, Models: []Model{{ID: "m", ModelKey: "model", DisplayName: "Model", Enabled: true}}}
	manifest.CredentialsBulkRequest.Items = []CredentialInput{{ID: "c", ProviderID: "p", EndpointID: "e", Name: "Key 1", APIKey: "secret-value", Priority: 1, Weight: 1, MaxConcurrency: 4}}
	manifest.Bindings = []BindingInput{{CredentialID: "c", ModelID: "m", UpstreamModel: "upstream-model"}}
	manifest.RoutingPolicies = []RoutingPolicy{{ModelID: "m", Strategy: "weighted_round_robin", MaxRetries: 2, FailureThreshold: 5, CooldownSeconds: 30}}
	b, _ := json.Marshal(manifest)
	if err := ImportManifestJSON(context.Background(), db, cipher, strings.NewReader(string(b))); err != nil {
		t.Fatal(err)
	}
	if err := ImportManifestJSON(context.Background(), db, cipher, strings.NewReader(string(b))); err != nil {
		t.Fatalf("second import: %v", err)
	}
	var raw []byte
	if err := db.QueryRow(`SELECT secret_ciphertext FROM ai_credentials WHERE id='c'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "secret-value") {
		t.Fatal("credential was stored in plaintext")
	}
	plain, err := cipher.Decrypt(raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != "secret-value" {
		t.Fatalf("unexpected decrypted secret %q", plain)
	}
	for table, want := range map[string]int{"ai_providers": 1, "ai_endpoints": 1, "ai_credentials": 1, "ai_models": 1, "ai_credential_models": 1, "ai_routing_policies": 1, "ai_credential_health": 1} {
		var got int
		if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s count=%d want=%d", table, got, want)
		}
	}
}

package airesource

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/thkx/agentmesh/internal/platform/cryptography"
)

// ImportManifest is the versioned-by-shape JSON document used for offline resource imports.
type ImportManifest struct {
	Provider               Provider `json:"provider"`
	Endpoint               Endpoint `json:"endpoint"`
	CredentialsBulkRequest struct {
		Items []CredentialInput `json:"items"`
	} `json:"credentials_bulk_request"`
	Models          []Model         `json:"models"`
	Bindings        []BindingInput  `json:"bindings"`
	RoutingPolicies []RoutingPolicy `json:"routing_policies"`
	ImportNotes     []string        `json:"import_notes,omitempty"`
}

// ImportManifestJSON imports a complete AI resource manifest atomically. Existing IDs are updated,
// making a repeated import safe. API keys are encrypted with the same AES-GCM cipher as the runtime.
func ImportManifestJSON(ctx context.Context, db *sql.DB, cipher *cryptography.AESGCM, src io.Reader) error {
	if db == nil || cipher == nil || src == nil {
		return errors.New("database, encryption cipher and manifest reader are required")
	}
	dec := json.NewDecoder(src)
	dec.DisallowUnknownFields()
	var m ImportManifest
	if err := dec.Decode(&m); err != nil {
		return fmt.Errorf("decode import manifest: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("import manifest must contain exactly one JSON value")
	}
	if err := m.validate(); err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin resource import: %w", err)
	}
	defer tx.Rollback()

	if err := importProvider(ctx, tx, m.Provider); err != nil {
		return err
	}
	if err := importEndpoint(ctx, tx, m.Endpoint); err != nil {
		return err
	}
	for _, item := range m.CredentialsBulkRequest.Items {
		enc, err := cipher.Encrypt([]byte(item.APIKey))
		if err != nil {
			return fmt.Errorf("encrypt credential %s: %w", item.ID, err)
		}
		now := nowMS()
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_credentials(id,provider_id,endpoint_id,name,secret_ciphertext,status,priority,weight,max_concurrency,created_at,updated_at)
			VALUES(?,?,?,?,?,'active',?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET provider_id=excluded.provider_id,endpoint_id=excluded.endpoint_id,name=excluded.name,secret_ciphertext=excluded.secret_ciphertext,status='active',priority=excluded.priority,weight=excluded.weight,max_concurrency=excluded.max_concurrency,updated_at=excluded.updated_at`,
			item.ID, item.ProviderID, item.EndpointID, item.Name, enc, item.Priority, item.Weight, item.MaxConcurrency, now, now)
		if err != nil {
			return fmt.Errorf("import credential %s: %w", item.ID, err)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO ai_credential_health(credential_id,updated_at) VALUES(?,?) ON CONFLICT(credential_id) DO NOTHING`, item.ID, now); err != nil {
			return fmt.Errorf("initialize credential health %s: %w", item.ID, err)
		}
	}
	for _, model := range m.Models {
		now := nowMS()
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_models(id,model_key,display_name,enabled,supports_streaming,created_at,updated_at) VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET model_key=excluded.model_key,display_name=excluded.display_name,enabled=excluded.enabled,supports_streaming=excluded.supports_streaming,updated_at=excluded.updated_at`,
			model.ID, model.ModelKey, model.DisplayName, boolInt(model.Enabled), boolInt(model.SupportsStreaming), now, now)
		if err != nil {
			return fmt.Errorf("import model %s: %w", model.ID, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO ai_routing_policies(model_id,strategy,max_retries,failure_threshold,cooldown_seconds,updated_at) VALUES(?, ?, ?, ?, ?, ?) ON CONFLICT(model_id) DO NOTHING`,
			model.ID,
			"weighted_round_robin",
			2,
			5,
			30,
			now,
		); err != nil {
			return fmt.Errorf("initialize routing policy %s: %w", model.ID, err)
		}
	}
	for _, binding := range m.Bindings {
		enabled := true
		if binding.Enabled != nil {
			enabled = *binding.Enabled
		}
		now := nowMS()
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_credential_models(credential_id,model_id,upstream_model,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?)
			ON CONFLICT(credential_id,model_id) DO UPDATE SET upstream_model=excluded.upstream_model,enabled=excluded.enabled,updated_at=excluded.updated_at`,
			binding.CredentialID, binding.ModelID, binding.UpstreamModel, boolInt(enabled), now, now)
		if err != nil {
			return fmt.Errorf("import binding %s/%s: %w", binding.CredentialID, binding.ModelID, err)
		}
	}
	for _, policy := range m.RoutingPolicies {
		now := nowMS()
		_, err := tx.ExecContext(ctx, `INSERT INTO ai_routing_policies(model_id,strategy,max_retries,failure_threshold,cooldown_seconds,updated_at) VALUES(?,?,?,?,?,?)
			ON CONFLICT(model_id) DO UPDATE SET strategy=excluded.strategy,max_retries=excluded.max_retries,failure_threshold=excluded.failure_threshold,cooldown_seconds=excluded.cooldown_seconds,updated_at=excluded.updated_at`,
			policy.ModelID, policy.Strategy, policy.MaxRetries, policy.FailureThreshold, policy.CooldownSeconds, now)
		if err != nil {
			return fmt.Errorf("import routing policy for %s: %w", policy.ModelID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit resource import: %w", err)
	}
	return nil
}

func (m ImportManifest) validate() error {
	if err := m.Provider.Validate(); err != nil {
		return fmt.Errorf("invalid provider: %w", err)
	}
	if err := m.Endpoint.Validate(); err != nil {
		return fmt.Errorf("invalid endpoint: %w", err)
	}
	if m.Endpoint.ProviderID != m.Provider.ID {
		return errors.New("endpoint provider_id does not match provider.id")
	}
	if len(m.CredentialsBulkRequest.Items) == 0 || len(m.CredentialsBulkRequest.Items) > 500 {
		return errors.New("manifest must contain 1..500 credentials")
	}
	if len(m.Models) == 0 {
		return errors.New("manifest must contain at least one model")
	}
	credentialIDs := map[string]bool{}
	for _, c := range m.CredentialsBulkRequest.Items {
		if err := c.Validate(); err != nil {
			return fmt.Errorf("invalid credential %s: %w", c.ID, err)
		}
		if c.ProviderID != m.Provider.ID || c.EndpointID != m.Endpoint.ID {
			return fmt.Errorf("credential %s references a different provider or endpoint", c.ID)
		}
		if credentialIDs[c.ID] {
			return fmt.Errorf("duplicate credential id %s", c.ID)
		}
		credentialIDs[c.ID] = true
	}
	modelIDs := map[string]bool{}
	modelKeys := map[string]bool{}
	for _, model := range m.Models {
		if err := model.Validate(); err != nil {
			return fmt.Errorf("invalid model: %w", err)
		}
		if modelIDs[model.ID] || modelKeys[model.ModelKey] {
			return fmt.Errorf("duplicate model id or model_key: %s", model.ID)
		}
		modelIDs[model.ID] = true
		modelKeys[model.ModelKey] = true
	}
	for _, b := range m.Bindings {
		if !credentialIDs[b.CredentialID] || !modelIDs[b.ModelID] || strings.TrimSpace(b.UpstreamModel) == "" {
			return fmt.Errorf("binding references unknown credential/model or has empty upstream_model: %s/%s", b.CredentialID, b.ModelID)
		}
	}
	for _, p := range m.RoutingPolicies {
		if !modelIDs[p.ModelID] {
			return fmt.Errorf("routing policy references unknown model %s", p.ModelID)
		}
		switch p.Strategy {
		case "priority", "round_robin", "weighted_round_robin", "least_connections":
		default:
			return fmt.Errorf("invalid routing strategy %q", p.Strategy)
		}
		if p.MaxRetries < 0 || p.MaxRetries > 5 || p.FailureThreshold < 1 || p.CooldownSeconds < 1 {
			return fmt.Errorf("invalid routing policy limits for model %s", p.ModelID)
		}
	}
	return nil
}

func importProvider(ctx context.Context, tx *sql.Tx, p Provider) error {
	now := nowMS()
	_, err := tx.ExecContext(ctx, `INSERT INTO ai_providers(id,name,protocol,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name,protocol=excluded.protocol,enabled=excluded.enabled,updated_at=excluded.updated_at`, p.ID, p.Name, p.Protocol, boolInt(p.Enabled), now, now)
	if err != nil {
		return fmt.Errorf("import provider: %w", err)
	}
	return nil
}
func importEndpoint(ctx context.Context, tx *sql.Tx, e Endpoint) error {
	now := nowMS()
	_, err := tx.ExecContext(ctx, `INSERT INTO ai_endpoints(id,provider_id,name,base_url,priority,timeout_ms,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET provider_id=excluded.provider_id,name=excluded.name,base_url=excluded.base_url,priority=excluded.priority,timeout_ms=excluded.timeout_ms,enabled=excluded.enabled,updated_at=excluded.updated_at`, e.ID, e.ProviderID, e.Name, strings.TrimRight(e.BaseURL, "/"), e.Priority, e.TimeoutMS, boolInt(e.Enabled), now, now)
	if err != nil {
		return fmt.Errorf("import endpoint: %w", err)
	}
	return nil
}

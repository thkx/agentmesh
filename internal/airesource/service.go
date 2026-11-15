package airesource

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/thkx/agentmesh/internal/platform/cryptography"
)

type Service struct {
	db     *sql.DB
	cipher *cryptography.AESGCM
}

func NewService(db *sql.DB, cipher *cryptography.AESGCM) (*Service, error) {
	if db == nil || cipher == nil {
		return nil, errors.New("AI resource service requires database and encryption")
	}
	return &Service{db: db, cipher: cipher}, nil
}
func newID(prefix string) string {
	b := make([]byte, 12)
	if _, e := rand.Read(b); e != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "_" + hex.EncodeToString(b)
}
func nowMS() int64 { return time.Now().UTC().UnixMilli() }
func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func (s *Service) CreateProvider(ctx context.Context, p Provider) error {
	if err := p.Validate(); err != nil {
		return err
	}
	n := nowMS()
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_providers(id,name,protocol,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?)`, p.ID, p.Name, p.Protocol, boolInt(p.Enabled), n, n)
	return err
}
func (s *Service) ListProviders(ctx context.Context) ([]Provider, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,name,protocol,enabled,created_at,updated_at FROM ai_providers ORDER BY name`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Provider{}
	for rows.Next() {
		var p Provider
		var en int
		var c, u int64
		if e = rows.Scan(&p.ID, &p.Name, &p.Protocol, &en, &c, &u); e != nil {
			return nil, e
		}
		p.Enabled = en == 1
		p.CreatedAt = time.UnixMilli(c).UTC()
		p.UpdatedAt = time.UnixMilli(u).UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Service) UpdateProvider(ctx context.Context, p Provider) error {
	if err := p.Validate(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE ai_providers SET name=?,protocol=?,enabled=?,updated_at=? WHERE id=?`, p.Name, p.Protocol, boolInt(p.Enabled), nowMS(), p.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Service) DeleteProvider(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ai_providers WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) CreateEndpoint(ctx context.Context, e Endpoint) error {
	if err := e.Validate(); err != nil {
		return err
	}
	n := nowMS()
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_endpoints(id,provider_id,name,base_url,priority,timeout_ms,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?)`, e.ID, e.ProviderID, e.Name, strings.TrimRight(e.BaseURL, "/"), e.Priority, e.TimeoutMS, boolInt(e.Enabled), n, n)
	return err
}
func (s *Service) UpdateEndpoint(ctx context.Context, e Endpoint) error {
	if err := e.Validate(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE ai_endpoints SET name=?,base_url=?,priority=?,timeout_ms=?,enabled=?,updated_at=? WHERE id=? AND provider_id=?`, e.Name, strings.TrimRight(e.BaseURL, "/"), e.Priority, e.TimeoutMS, boolInt(e.Enabled), nowMS(), e.ID, e.ProviderID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Service) DeleteEndpoint(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ai_endpoints WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) ListEndpoints(ctx context.Context) ([]Endpoint, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,provider_id,name,base_url,priority,timeout_ms,enabled,created_at,updated_at FROM ai_endpoints ORDER BY priority,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Endpoint{}
	for rows.Next() {
		var x Endpoint
		var en int
		var c, u int64
		if e = rows.Scan(&x.ID, &x.ProviderID, &x.Name, &x.BaseURL, &x.Priority, &x.TimeoutMS, &en, &c, &u); e != nil {
			return nil, e
		}
		x.Enabled = en == 1
		x.CreatedAt = time.UnixMilli(c).UTC()
		x.UpdatedAt = time.UnixMilli(u).UTC()
		out = append(out, x)
	}
	return out, rows.Err()
}
func maskKey(v string) string {
	if len(v) <= 8 {
		return "****"
	}
	return "****" + v[len(v)-4:]
}
func (s *Service) CreateCredential(ctx context.Context, in CredentialInput) error {
	if err := in.Validate(); err != nil {
		return err
	}
	encrypted, err := s.cipher.Encrypt([]byte(in.APIKey))
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	n := nowMS()
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_credentials(id,provider_id,endpoint_id,name,secret_ciphertext,status,priority,weight,max_concurrency,created_at,updated_at) VALUES(?,?,?,?,?,'active',?,?,?,?,?)`, in.ID, in.ProviderID, in.EndpointID, in.Name, encrypted, in.Priority, in.Weight, in.MaxConcurrency, n, n); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO ai_credential_health(credential_id,updated_at) VALUES(?,?)`, in.ID, n); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) CreateCredentialsBulk(ctx context.Context, items []CredentialInput) error {
	if len(items) == 0 || len(items) > 500 {
		return fmt.Errorf("bulk import requires 1..500 credentials")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := nowMS()
	for _, item := range items {
		if err := item.Validate(); err != nil {
			return err
		}
		encrypted, err := s.cipher.Encrypt([]byte(item.APIKey))
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO ai_credentials(id,provider_id,endpoint_id,name,secret_ciphertext,status,priority,weight,max_concurrency,created_at,updated_at) VALUES(?,?,?,?,?,'active',?,?,?,?,?)`, item.ID, item.ProviderID, item.EndpointID, item.Name, encrypted, item.Priority, item.Weight, item.MaxConcurrency, now, now); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO ai_credential_health(credential_id,updated_at) VALUES(?,?)`, item.ID, now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Service) ListCredentials(ctx context.Context) ([]Credential, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,provider_id,endpoint_id,name,status,priority,weight,max_concurrency,created_at,updated_at FROM ai_credentials ORDER BY priority,id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Credential{}
	for rows.Next() {
		var c Credential
		var cr, up int64
		if e = rows.Scan(&c.ID, &c.ProviderID, &c.EndpointID, &c.Name, &c.Status, &c.Priority, &c.Weight, &c.MaxConcurrency, &cr, &up); e != nil {
			return nil, e
		}
		c.CreatedAt = time.UnixMilli(cr).UTC()
		c.UpdatedAt = time.UnixMilli(up).UTC()
		var enc []byte
		if e = s.db.QueryRowContext(ctx, `SELECT secret_ciphertext FROM ai_credentials WHERE id=?`, c.ID).Scan(&enc); e != nil {
			return nil, e
		}
		plain, de := s.cipher.Decrypt(enc)
		if de != nil {
			return nil, fmt.Errorf("decrypt credential metadata %s: %w", c.ID, de)
		}
		c.MaskedKey = maskKey(string(plain))
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Service) SetCredentialStatus(ctx context.Context, id, status string) error {
	if status != "active" && status != "disabled" {
		return fmt.Errorf("status must be active or disabled")
	}
	res, e := s.db.ExecContext(ctx, `UPDATE ai_credentials SET status=?,updated_at=? WHERE id=?`, status, nowMS(), id)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	if status == "active" {
		_, e = s.db.ExecContext(ctx, `UPDATE ai_credential_health SET state='healthy',consecutive_failures=0,cooldown_until=0,last_error='',updated_at=? WHERE credential_id=?`, nowMS(), id)
	}
	return e
}
func (s *Service) RotateCredential(ctx context.Context, id, name, apiKey string, priority, weight, maxConcurrency int) error {
	if strings.TrimSpace(apiKey) == "" || strings.TrimSpace(name) == "" || weight <= 0 || maxConcurrency <= 0 {
		return fmt.Errorf("name, api_key, positive weight and max_concurrency are required")
	}
	encrypted, err := s.cipher.Encrypt([]byte(apiKey))
	if err != nil {
		return err
	}
	n := nowMS()
	res, err := s.db.ExecContext(ctx, `UPDATE ai_credentials SET name=?,secret_ciphertext=?,status='active',priority=?,weight=?,max_concurrency=?,updated_at=? WHERE id=?`, name, encrypted, priority, weight, maxConcurrency, n, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	_, err = s.db.ExecContext(ctx, `UPDATE ai_credential_health SET state='healthy',consecutive_failures=0,cooldown_until=0,last_error='',updated_at=? WHERE credential_id=?`, n, id)
	return err
}

func (s *Service) DeleteCredential(ctx context.Context, id string) error {
	res, e := s.db.ExecContext(ctx, `DELETE FROM ai_credentials WHERE id=?`, id)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Service) CreateModel(ctx context.Context, m Model) error {
	if err := m.Validate(); err != nil {
		return err
	}
	n := nowMS()
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_models(id,model_key,display_name,enabled,supports_streaming,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, m.ID, m.ModelKey, m.DisplayName, boolInt(m.Enabled), boolInt(m.SupportsStreaming), n, n)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO ai_routing_policies(model_id, strategy, max_retries,failure_threshold, cooldown_seconds, updated_at) VALUES(?, ?, ?, ?, ?, ?)`,
		m.ID, "weighted_round_robin", 2, 5, 30, n,
	)
	return err
}
func (s *Service) ListModels(ctx context.Context) ([]Model, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT id,model_key,display_name,enabled,supports_streaming,created_at,updated_at FROM ai_models ORDER BY model_key`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Model{}
	for rows.Next() {
		var m Model
		var en, st int
		var c, u int64
		if e = rows.Scan(&m.ID, &m.ModelKey, &m.DisplayName, &en, &st, &c, &u); e != nil {
			return nil, e
		}
		m.Enabled = en == 1
		m.SupportsStreaming = st == 1
		m.CreatedAt = time.UnixMilli(c).UTC()
		m.UpdatedAt = time.UnixMilli(u).UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}
// GetModelByID resolves a configured AI Resource model by its stable internal ID.
func (s *Service) GetModelByID(ctx context.Context, id string) (Model, error) {
	var m Model
	var enabled, streaming int
	var created, updated int64
	err := s.db.QueryRowContext(ctx, `SELECT id,model_key,display_name,enabled,supports_streaming,created_at,updated_at FROM ai_models WHERE id=?`, id).
		Scan(&m.ID, &m.ModelKey, &m.DisplayName, &enabled, &streaming, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Model{}, ErrNotFound
	}
	if err != nil {
		return Model{}, err
	}
	m.Enabled = enabled == 1
	m.SupportsStreaming = streaming == 1
	m.CreatedAt = time.UnixMilli(created).UTC()
	m.UpdatedAt = time.UnixMilli(updated).UTC()
	return m, nil
}

func (s *Service) BindModel(ctx context.Context, b BindingInput) error {
	if b.CredentialID == "" || b.ModelID == "" || strings.TrimSpace(b.UpstreamModel) == "" {
		return fmt.Errorf("credential_id, model_id and upstream_model are required")
	}
	enabled := true
	if b.Enabled != nil {
		enabled = *b.Enabled
	}
	n := nowMS()
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_credential_models(credential_id,model_id,upstream_model,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?) ON CONFLICT(credential_id,model_id) DO UPDATE SET upstream_model=excluded.upstream_model,enabled=excluded.enabled,updated_at=excluded.updated_at`, b.CredentialID, b.ModelID, b.UpstreamModel, boolInt(enabled), n, n)
	return err
}
func (s *Service) UpdateModel(ctx context.Context, m Model) error {
	if err := m.Validate(); err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE ai_models SET display_name=?,enabled=?,supports_streaming=?,updated_at=? WHERE id=?`, m.DisplayName, boolInt(m.Enabled), boolInt(m.SupportsStreaming), nowMS(), m.ID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Service) DeleteModel(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM ai_models WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Service) ListBindings(ctx context.Context) ([]BindingInput, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT credential_id,model_id,upstream_model,enabled FROM ai_credential_models ORDER BY model_id,credential_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []BindingInput{}
	for rows.Next() {
		var b BindingInput
		var en int
		if e = rows.Scan(&b.CredentialID, &b.ModelID, &b.UpstreamModel, &en); e != nil {
			return nil, e
		}
		v := en == 1
		b.Enabled = &v
		out = append(out, b)
	}
	return out, rows.Err()
}
func (s *Service) SetPolicy(ctx context.Context, p RoutingPolicy) error {
	switch p.Strategy {
	case "priority", "round_robin", "weighted_round_robin", "least_connections":
	default:
		return fmt.Errorf("invalid routing strategy")
	}
	if p.MaxRetries < 0 || p.MaxRetries > 5 || p.FailureThreshold < 1 || p.CooldownSeconds < 1 {
		return fmt.Errorf("invalid routing policy limits")
	}
	res, e := s.db.ExecContext(ctx, `UPDATE ai_routing_policies SET strategy=?,max_retries=?,failure_threshold=?,cooldown_seconds=?,updated_at=? WHERE model_id=?`, p.Strategy, p.MaxRetries, p.FailureThreshold, p.CooldownSeconds, nowMS(), p.ModelID)
	if e != nil {
		return e
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (s *Service) ListPolicies(ctx context.Context) ([]RoutingPolicy, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT model_id,strategy,max_retries,failure_threshold,cooldown_seconds,updated_at FROM ai_routing_policies ORDER BY model_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []RoutingPolicy{}
	for rows.Next() {
		var p RoutingPolicy
		var n int64
		if e = rows.Scan(&p.ModelID, &p.Strategy, &p.MaxRetries, &p.FailureThreshold, &p.CooldownSeconds, &n); e != nil {
			return nil, e
		}
		p.UpdatedAt = time.UnixMilli(n).UTC()
		out = append(out, p)
	}
	return out, rows.Err()
}
func (s *Service) Candidates(ctx context.Context, modelKey string) ([]Candidate, RoutingPolicy, error) {
	var model Model
	var en, st int
	var c, u int64
	e := s.db.QueryRowContext(ctx, `SELECT id,model_key,display_name,enabled,supports_streaming,created_at,updated_at FROM ai_models WHERE model_key=?`, modelKey).Scan(&model.ID, &model.ModelKey, &model.DisplayName, &en, &st, &c, &u)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, RoutingPolicy{}, ErrNotFound
	}
	if e != nil {
		return nil, RoutingPolicy{}, e
	}
	model.Enabled = en == 1
	model.SupportsStreaming = st == 1
	if !model.Enabled {
		return nil, RoutingPolicy{}, fmt.Errorf("model is disabled")
	}
	var policy RoutingPolicy
	e = s.db.QueryRowContext(ctx, `SELECT model_id,strategy,max_retries,failure_threshold,cooldown_seconds,updated_at FROM ai_routing_policies WHERE model_id=?`, model.ID).Scan(&policy.ModelID, &policy.Strategy, &policy.MaxRetries, &policy.FailureThreshold, &policy.CooldownSeconds, &u)
	if e != nil {
		return nil, policy, e
	}
	rows, e := s.db.QueryContext(ctx, `SELECT c.id,c.provider_id,c.endpoint_id,c.name,c.status,c.priority,c.weight,c.max_concurrency,c.secret_ciphertext,p.name,p.protocol,p.enabled,e.name,e.base_url,e.priority,e.timeout_ms,e.enabled,b.upstream_model,COALESCE(h.state,'healthy'),COALESCE(h.consecutive_failures,0),COALESCE(h.cooldown_until,0),COALESCE(h.last_error,''),COALESCE(h.last_status_code,0),COALESCE(h.last_latency_ms,0) FROM ai_credential_models b JOIN ai_credentials c ON c.id=b.credential_id JOIN ai_providers p ON p.id=c.provider_id JOIN ai_endpoints e ON e.id=c.endpoint_id LEFT JOIN ai_credential_health h ON h.credential_id=c.id WHERE b.model_id=? AND b.enabled=1 AND c.status='active' AND p.enabled=1 AND e.enabled=1 ORDER BY c.priority,e.priority,c.id`, model.ID)
	if e != nil {
		return nil, policy, e
	}
	defer rows.Close()
	out := []Candidate{}
	for rows.Next() {
		var x Candidate
		var protocol Protocol
		var pen, een int
		var cooldown int64
		var ciphertext []byte
		if e = rows.Scan(&x.Credential.ID, &x.Credential.ProviderID, &x.Credential.EndpointID, &x.Credential.Name, &x.Credential.Status, &x.Credential.Priority, &x.Credential.Weight, &x.Credential.MaxConcurrency, &ciphertext, &x.Provider.Name, &protocol, &pen, &x.Endpoint.Name, &x.Endpoint.BaseURL, &x.Endpoint.Priority, &x.Endpoint.TimeoutMS, &een, &x.UpstreamModel, &x.Health.State, &x.Health.ConsecutiveFailures, &cooldown, &x.Health.LastError, &x.Health.LastStatusCode, &x.Health.LastLatencyMS); e != nil {
			return nil, policy, e
		}
		x.Provider.ID = x.Credential.ProviderID
		x.Provider.Protocol = protocol
		x.Provider.Enabled = pen == 1
		x.Endpoint.ID = x.Credential.EndpointID
		x.Endpoint.ProviderID = x.Credential.ProviderID
		x.Endpoint.Enabled = een == 1
		x.Model = model
		x.Health.CredentialID = x.Credential.ID
		if cooldown > 0 {
			x.Health.CooldownUntil = time.UnixMilli(cooldown).UTC()
		}
		plain, de := s.cipher.Decrypt(ciphertext)
		if de != nil {
			return nil, policy, fmt.Errorf("decrypt credential %s: %w", x.Credential.ID, de)
		}
		x.Secret = string(plain)
		x.Credential.MaskedKey = maskKey(x.Secret)
		out = append(out, x)
	}
	if e = rows.Err(); e != nil {
		return nil, policy, e
	}
	return out, policy, nil
}
func (s *Service) RecordResult(ctx context.Context, c Candidate, requestID string, statusCode int, latency time.Duration, callErr error, usage UsageTokens, policy RoutingPolicy) error {
	n := nowMS()
	status := "success"
	errorCode := ""
	if callErr != nil {
		status = "failure"
		errorCode = classifyError(statusCode)
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	_, e = tx.ExecContext(ctx, `INSERT INTO ai_request_logs(id,model_id,credential_id,provider_id,endpoint_id,status,status_code,latency_ms,error_code,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, requestID, c.Model.ID, c.Credential.ID, c.Provider.ID, c.Endpoint.ID, status, statusCode, latency.Milliseconds(), errorCode, n)
	if e != nil {
		return e
	}
	if callErr == nil {
		_, e = tx.ExecContext(ctx, `UPDATE ai_credential_health SET state='healthy',consecutive_failures=0,cooldown_until=0,last_error='',last_status_code=?,last_latency_ms=?,last_success_at=?,updated_at=? WHERE credential_id=?`, statusCode, latency.Milliseconds(), n, n, c.Credential.ID)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO ai_usage_records(id,request_id,input_tokens,output_tokens,total_tokens,created_at) VALUES(?,?,?,?,?,?)`, newID("usage"), requestID, usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens, n)
		if e != nil {
			return e
		}
	} else {
		state := "degraded"
		cool := int64(0)
		if statusCode == 401 || statusCode == 403 {
			state = "degraded"
		}
		if statusCode == 429 {
			state = "open"
			cool = time.Now().Add(time.Duration(policy.CooldownSeconds) * time.Second).UnixMilli()
		}
		var failures int
		_ = tx.QueryRowContext(ctx, `SELECT consecutive_failures FROM ai_credential_health WHERE credential_id=?`, c.Credential.ID).Scan(&failures)
		failures++
		if failures >= policy.FailureThreshold {
			state = "open"
			cool = time.Now().Add(time.Duration(policy.CooldownSeconds) * time.Second).UnixMilli()
		}
		_, e = tx.ExecContext(ctx, `UPDATE ai_credential_health SET state=?,failure_count=failure_count+1,consecutive_failures=?,cooldown_until=?,last_error=?,last_status_code=?,last_latency_ms=?,last_failure_at=?,updated_at=? WHERE credential_id=?`, state, failures, cool, errorCode, statusCode, latency.Milliseconds(), n, n, c.Credential.ID)
		if e != nil {
			return e
		}
		if statusCode == 401 {
			_, e = tx.ExecContext(ctx, `UPDATE ai_credentials SET status='invalid',updated_at=? WHERE id=?`, n, c.Credential.ID)
			if e != nil {
				return e
			}
		}
	}
	return tx.Commit()
}
func classifyError(code int) string {
	switch code {
	case 401:
		return "authentication_failed"
	case 403:
		return "permission_denied"
	case 429:
		return "rate_limited"
	case 500, 502, 503, 504:
		return "provider_unavailable"
	case 0:
		return "network_or_timeout"
	default:
		return "provider_error"
	}
}
func (s *Service) Health(ctx context.Context) ([]Health, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT credential_id,state,failure_count,consecutive_failures,cooldown_until,last_error,last_status_code,last_latency_ms,last_success_at,last_failure_at FROM ai_credential_health ORDER BY credential_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Health{}
	for rows.Next() {
		var h Health
		var cd, ok, fail int64
		if e = rows.Scan(&h.CredentialID, &h.State, &h.FailureCount, &h.ConsecutiveFailures, &cd, &h.LastError, &h.LastStatusCode, &h.LastLatencyMS, &ok, &fail); e != nil {
			return nil, e
		}
		if cd > 0 {
			h.CooldownUntil = time.UnixMilli(cd).UTC()
		}
		if ok > 0 {
			h.LastSuccessAt = time.UnixMilli(ok).UTC()
		}
		if fail > 0 {
			h.LastFailureAt = time.UnixMilli(fail).UTC()
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
func (s *Service) ListRequests(ctx context.Context, limit int) ([]RequestLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,model_id,credential_id,provider_id,endpoint_id,status,status_code,latency_ms,error_code,created_at FROM ai_request_logs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RequestLog{}
	for rows.Next() {
		var v RequestLog
		var created int64
		if err = rows.Scan(&v.ID, &v.ModelID, &v.CredentialID, &v.ProviderID, &v.EndpointID, &v.Status, &v.StatusCode, &v.LatencyMS, &v.ErrorCode, &created); err != nil {
			return nil, err
		}
		v.CreatedAt = time.UnixMilli(created).UTC()
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Service) Usage(ctx context.Context) (Usage, error) {
	var u Usage
	e := s.db.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(CASE WHEN status='success' THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN status='failure' THEN 1 ELSE 0 END),0) FROM ai_request_logs`).Scan(&u.Requests, &u.Successes, &u.Failures)
	if e != nil {
		return u, e
	}
	e = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),COALESCE(SUM(total_tokens),0) FROM ai_usage_records`).Scan(&u.InputTokens, &u.OutputTokens, &u.TotalTokens)
	return u, e
}
func (s *Service) DiscoverModels(ctx context.Context, id string) ([]string, error) {
	var status string
	var cipherText []byte
	var protocol Protocol
	var baseURL string
	var timeout int
	err := s.db.QueryRowContext(ctx, `SELECT c.status,c.secret_ciphertext,p.protocol,e.base_url,e.timeout_ms FROM ai_credentials c JOIN ai_providers p ON p.id=c.provider_id JOIN ai_endpoints e ON e.id=c.endpoint_id WHERE c.id=?`, id).Scan(&status, &cipherText, &protocol, &baseURL, &timeout)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != "active" {
		return nil, fmt.Errorf("credential is not active")
	}
	secret, err := s.cipher.Decrypt(cipherText)
	if err != nil {
		return nil, err
	}
	return discoverModels(ctx, protocol, baseURL, string(secret), time.Duration(timeout)*time.Millisecond)
}
func (s *Service) CheckCredential(ctx context.Context, id string) error {
	err := s.TestCredential(ctx, id)
	state := "healthy"
	message := ""
	if err != nil {
		state = "degraded"
		message = classifyError(statusFromError(err))
	}
	_, updateErr := s.db.ExecContext(ctx, `UPDATE ai_credential_health SET state=?,last_error=?,updated_at=? WHERE credential_id=?`, state, message, nowMS(), id)
	return errors.Join(err, updateErr)
}
func statusFromError(err error) int {
	var upstream *upstreamError
	if errors.As(err, &upstream) {
		return upstream.Status
	}
	return 0
}

func (s *Service) TestCredential(ctx context.Context, id string) error {
	var status string
	var endpointID string
	var cipherText []byte
	var protocol Protocol
	var baseURL string
	var timeout int
	e := s.db.QueryRowContext(ctx, `SELECT c.status,c.endpoint_id,c.secret_ciphertext,p.protocol,e.base_url,e.timeout_ms FROM ai_credentials c JOIN ai_providers p ON p.id=c.provider_id JOIN ai_endpoints e ON e.id=c.endpoint_id WHERE c.id=?`, id).Scan(&status, &endpointID, &cipherText, &protocol, &baseURL, &timeout)
	if errors.Is(e, sql.ErrNoRows) {
		return ErrNotFound
	}
	if e != nil {
		return e
	}
	if status != "active" {
		return fmt.Errorf("credential is not active")
	}
	secret, e := s.cipher.Decrypt(cipherText)
	if e != nil {
		return e
	}
	return probeEndpoint(ctx, protocol, baseURL, string(secret), time.Duration(timeout)*time.Millisecond)
}


// HasModelKey reports whether a dynamic resource model is configured. A
// configured-but-disabled model remains owned by the dynamic router: requests
// fail closed rather than silently falling back to a static credential.
func (s *Service) HasModelKey(ctx context.Context, modelKey string) (bool, error) {
	if strings.TrimSpace(modelKey) == "" {
		return false, nil
	}
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM ai_models WHERE model_key = ?)`, modelKey).Scan(&exists); err != nil {
		return false, err
	}
	return exists == 1, nil
}

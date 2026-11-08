-- name: CreateAIProvider :exec
INSERT INTO ai_providers (id, name, protocol, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?);

-- name: ListAIProviders :many
SELECT id, name, protocol, enabled, created_at, updated_at
FROM ai_providers ORDER BY name;

-- name: CreateAIEndpoint :exec
INSERT INTO ai_endpoints (id, provider_id, name, base_url, priority, timeout_ms, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListAICredentials :many
SELECT id, provider_id, endpoint_id, name, status, priority, weight, max_concurrency, created_at, updated_at
FROM ai_credentials ORDER BY priority, id;

-- name: ListAIModels :many
SELECT id, model_key, display_name, enabled, supports_streaming, created_at, updated_at
FROM ai_models ORDER BY model_key;

-- name: UpsertAICredentialModel :exec
INSERT INTO ai_credential_models (credential_id, model_id, upstream_model, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(credential_id, model_id) DO UPDATE SET
  upstream_model = excluded.upstream_model,
  enabled = excluded.enabled,
  updated_at = excluded.updated_at;

-- name: ListAIHealth :many
SELECT credential_id, state, failure_count, consecutive_failures, cooldown_until,
       last_error, last_status_code, last_latency_ms, last_success_at, last_failure_at
FROM ai_credential_health ORDER BY credential_id;

-- name: ListAIRequestUsage :one
SELECT COUNT(*) AS requests,
       COALESCE(SUM(CASE WHEN status = 'success' THEN 1 ELSE 0 END), 0) AS successes,
       COALESCE(SUM(CASE WHEN status = 'failure' THEN 1 ELSE 0 END), 0) AS failures
FROM ai_request_logs;

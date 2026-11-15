CREATE TABLE ai_providers (
    id TEXT PRIMARY KEY NOT NULL,
    name TEXT NOT NULL,
    protocol TEXT NOT NULL CHECK (protocol IN ('openai-compatible','anthropic','gemini')),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE ai_endpoints (
    id TEXT PRIMARY KEY NOT NULL,
    provider_id TEXT NOT NULL,
    name TEXT NOT NULL,
    base_url TEXT NOT NULL,
    priority INTEGER NOT NULL DEFAULT 100,
    timeout_ms INTEGER NOT NULL DEFAULT 60000,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(id, provider_id),
    FOREIGN KEY(provider_id) REFERENCES ai_providers(id) ON DELETE RESTRICT
);
CREATE INDEX idx_ai_endpoints_provider ON ai_endpoints(provider_id, enabled, priority);
CREATE TABLE ai_credentials (
    id TEXT PRIMARY KEY NOT NULL,
    provider_id TEXT NOT NULL,
    endpoint_id TEXT NOT NULL,
    name TEXT NOT NULL,
    secret_ciphertext BLOB NOT NULL,
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled','invalid')),
    priority INTEGER NOT NULL DEFAULT 100,
    weight INTEGER NOT NULL DEFAULT 1 CHECK (weight > 0),
    max_concurrency INTEGER NOT NULL DEFAULT 4 CHECK (max_concurrency > 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY(provider_id) REFERENCES ai_providers(id) ON DELETE RESTRICT,
    FOREIGN KEY(endpoint_id, provider_id) REFERENCES ai_endpoints(id, provider_id) ON DELETE RESTRICT
);
CREATE INDEX idx_ai_credentials_provider ON ai_credentials(provider_id, status, priority);
CREATE INDEX idx_ai_credentials_endpoint ON ai_credentials(endpoint_id);
CREATE TABLE ai_models (
    id TEXT PRIMARY KEY NOT NULL,
    model_key TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    supports_streaming INTEGER NOT NULL DEFAULT 1 CHECK (supports_streaming IN (0,1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE TABLE ai_credential_models (
    credential_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    upstream_model TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0,1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY(credential_id, model_id),
    FOREIGN KEY(credential_id) REFERENCES ai_credentials(id) ON DELETE CASCADE,
    FOREIGN KEY(model_id) REFERENCES ai_models(id) ON DELETE CASCADE
);
CREATE INDEX idx_ai_credential_models_model ON ai_credential_models(model_id, enabled);
CREATE TABLE ai_routing_policies (
    model_id TEXT PRIMARY KEY NOT NULL,
    strategy TEXT NOT NULL DEFAULT 'weighted_round_robin' CHECK (strategy IN ('priority','round_robin','weighted_round_robin','least_connections')),
    max_retries INTEGER NOT NULL DEFAULT 2 CHECK (max_retries BETWEEN 0 AND 5),
    failure_threshold INTEGER NOT NULL DEFAULT 5 CHECK (failure_threshold > 0),
    cooldown_seconds INTEGER NOT NULL DEFAULT 30 CHECK (cooldown_seconds > 0),
    updated_at INTEGER NOT NULL,
    FOREIGN KEY(model_id) REFERENCES ai_models(id) ON DELETE CASCADE
);
CREATE TABLE ai_credential_health (
    credential_id TEXT PRIMARY KEY NOT NULL,
    state TEXT NOT NULL DEFAULT 'healthy' CHECK (state IN ('healthy','degraded','open','half_open')),
    failure_count INTEGER NOT NULL DEFAULT 0,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    cooldown_until INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    last_status_code INTEGER NOT NULL DEFAULT 0,
    last_latency_ms INTEGER NOT NULL DEFAULT 0,
    last_success_at INTEGER NOT NULL DEFAULT 0,
    last_failure_at INTEGER NOT NULL DEFAULT 0,
    updated_at INTEGER NOT NULL,
    FOREIGN KEY(credential_id) REFERENCES ai_credentials(id) ON DELETE CASCADE
);
CREATE TABLE ai_request_logs (
    id TEXT PRIMARY KEY NOT NULL,
    model_id TEXT NOT NULL,
    credential_id TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    endpoint_id TEXT NOT NULL,
    status TEXT NOT NULL,
    status_code INTEGER NOT NULL DEFAULT 0,
    latency_ms INTEGER NOT NULL DEFAULT 0,
    error_code TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    FOREIGN KEY(model_id) REFERENCES ai_models(id) ON DELETE RESTRICT,
    FOREIGN KEY(credential_id) REFERENCES ai_credentials(id) ON DELETE RESTRICT
);
CREATE INDEX idx_ai_request_logs_created ON ai_request_logs(created_at DESC);
CREATE INDEX idx_ai_request_logs_model ON ai_request_logs(model_id, created_at DESC);
CREATE TABLE ai_usage_records (
    id TEXT PRIMARY KEY NOT NULL,
    request_id TEXT NOT NULL UNIQUE,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    FOREIGN KEY(request_id) REFERENCES ai_request_logs(id) ON DELETE CASCADE
);

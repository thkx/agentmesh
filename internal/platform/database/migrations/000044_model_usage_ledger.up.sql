CREATE TABLE model_usage_events (
    id TEXT PRIMARY KEY,
    idempotency_key TEXT NOT NULL UNIQUE,
    provider_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    input_tokens INTEGER,
    output_tokens INTEGER,
    total_tokens INTEGER,
    cached_input_tokens INTEGER,
    reasoning_tokens INTEGER,
    usage_source TEXT NOT NULL CHECK (usage_source IN ('provider_reported','estimated','unavailable')),
    currency TEXT,
    input_rate_micros_per_million INTEGER,
    output_rate_micros_per_million INTEGER,
    estimated_cost_micros INTEGER,
    request_id TEXT,
    trace_id TEXT,
    execution_id TEXT,
    attempt_id TEXT,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_model_usage_events_occurred_at ON model_usage_events(occurred_at DESC);
CREATE INDEX idx_model_usage_events_provider_model ON model_usage_events(provider_id, model_id, occurred_at DESC);
CREATE INDEX idx_model_usage_events_execution ON model_usage_events(execution_id) WHERE execution_id IS NOT NULL;

CREATE TABLE model_pricing_rates (
    id TEXT PRIMARY KEY,
    provider_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    currency TEXT NOT NULL CHECK (length(currency) = 3),
    input_rate_micros_per_million INTEGER NOT NULL CHECK (input_rate_micros_per_million >= 0),
    output_rate_micros_per_million INTEGER NOT NULL CHECK (output_rate_micros_per_million >= 0),
    effective_at TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_model_pricing_rates_lookup ON model_pricing_rates(provider_id, model_id, effective_at DESC);

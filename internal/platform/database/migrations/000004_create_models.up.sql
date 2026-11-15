CREATE TABLE models (
    id TEXT PRIMARY KEY NOT NULL,
    provider_id TEXT NOT NULL,
    name TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    context_window INTEGER NOT NULL DEFAULT 0 CHECK (context_window >= 0),
    max_output_tokens INTEGER NOT NULL DEFAULT 0 CHECK (max_output_tokens >= 0),
    supports_streaming INTEGER NOT NULL DEFAULT 0 CHECK (supports_streaming IN (0, 1)),
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT fk_models_provider FOREIGN KEY (provider_id)
        REFERENCES providers(id) ON UPDATE CASCADE ON DELETE RESTRICT,
    CHECK (context_window = 0 OR max_output_tokens <= context_window)
);
CREATE INDEX idx_models_provider_id ON models(provider_id);
CREATE INDEX idx_models_enabled ON models(enabled);

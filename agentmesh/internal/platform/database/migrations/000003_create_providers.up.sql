CREATE TABLE providers (
    id TEXT PRIMARY KEY NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('openai', 'anthropic', 'gemini', 'ollama')),
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('cloud', 'local')),
    base_url TEXT NOT NULL DEFAULT '',
    credential_ref TEXT NOT NULL DEFAULT '',
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
CREATE INDEX idx_providers_type ON providers(type);
CREATE INDEX idx_providers_enabled ON providers(enabled);

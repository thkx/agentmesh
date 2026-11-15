CREATE TABLE provider_credentials (
    provider_id TEXT PRIMARY KEY NOT NULL,
    encrypted_value BLOB NOT NULL,
    encryption_version INTEGER NOT NULL CHECK (encryption_version > 0),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    CONSTRAINT fk_provider_credentials_provider FOREIGN KEY (provider_id)
        REFERENCES providers(id) ON UPDATE CASCADE ON DELETE CASCADE
);

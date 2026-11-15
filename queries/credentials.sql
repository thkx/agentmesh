-- name: UpsertProviderCredential :exec
INSERT INTO provider_credentials (provider_id, encrypted_value, encryption_version, created_at, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(provider_id) DO UPDATE SET
    encrypted_value = excluded.encrypted_value,
    encryption_version = excluded.encryption_version,
    updated_at = excluded.updated_at;

-- name: GetProviderCredential :one
SELECT provider_id, encrypted_value, encryption_version, created_at, updated_at
FROM provider_credentials WHERE provider_id = ?;

-- name: DeleteProviderCredential :exec
DELETE FROM provider_credentials WHERE provider_id = ?;

-- name: ProviderCredentialExists :one
SELECT EXISTS(SELECT 1 FROM provider_credentials WHERE provider_id = ?) AS present;

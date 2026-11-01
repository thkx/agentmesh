-- name: UpsertProvider :exec
INSERT INTO providers (id, type, name, kind, base_url, credential_ref, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    type = excluded.type,
    name = excluded.name,
    kind = excluded.kind,
    base_url = excluded.base_url,
    credential_ref = excluded.credential_ref,
    enabled = excluded.enabled,
    updated_at = excluded.updated_at;

-- name: GetProvider :one
SELECT id, type, name, kind, base_url, credential_ref, enabled, created_at, updated_at
FROM providers WHERE id = ?;

-- name: ListProviders :many
SELECT id, type, name, kind, base_url, credential_ref, enabled, created_at, updated_at
FROM providers ORDER BY id;

-- name: DeleteProvider :exec
DELETE FROM providers WHERE id = ?;

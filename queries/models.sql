-- name: UpsertModel :exec
INSERT INTO models (id, provider_id, name, display_name, context_window, max_output_tokens, supports_streaming, enabled, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    provider_id = excluded.provider_id,
    name = excluded.name,
    display_name = excluded.display_name,
    context_window = excluded.context_window,
    max_output_tokens = excluded.max_output_tokens,
    supports_streaming = excluded.supports_streaming,
    enabled = excluded.enabled,
    updated_at = excluded.updated_at;

-- name: GetModel :one
SELECT id, provider_id, name, display_name, context_window, max_output_tokens, supports_streaming, enabled, created_at, updated_at
FROM models WHERE id = ?;

-- name: ListModels :many
SELECT id, provider_id, name, display_name, context_window, max_output_tokens, supports_streaming, enabled, created_at, updated_at
FROM models ORDER BY id;

-- name: DeleteModel :exec
DELETE FROM models WHERE id = ?;

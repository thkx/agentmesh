-- name: CreateUser :exec
INSERT INTO users (email, display_name, created_at)
VALUES (?, ?, ?);

-- name: GetUser :one
SELECT id, email, display_name, created_at FROM users WHERE id = ?;

-- name: GetUserByEmail :one
SELECT id, email, display_name, created_at FROM users WHERE email = ?;

-- name: ListUsers :many
SELECT id, email, display_name, created_at FROM users ORDER BY id;

-- name: UpdateUserDisplayName :execrows
UPDATE users SET display_name = ? WHERE id = ?;

-- name: DeleteUser :exec
DELETE FROM users WHERE id = ?;

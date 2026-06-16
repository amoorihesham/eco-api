-- name: CreateUser :exec
INSERT INTO identity_users (id, email, password_hash, name, role, created_at)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetUserByEmail :one
SELECT id, email, password_hash, name, role, created_at
FROM identity_users WHERE email = $1;

-- name: GetUserByID :one
SELECT id, email, password_hash, name, role, created_at
FROM identity_users WHERE id = $1;

-- name: UpdatePasswordHash :exec
UPDATE identity_users SET password_hash = $2 WHERE id = $1;

-- name: InsertRefreshToken :exec
INSERT INTO identity_refresh_tokens (id, user_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4);

-- name: GetRefreshToken :one
SELECT id, user_id, token_hash, expires_at
FROM identity_refresh_tokens WHERE token_hash = $1;

-- name: DeleteRefreshToken :exec
DELETE FROM identity_refresh_tokens WHERE token_hash = $1;

-- name: DeleteUserRefreshTokens :exec
DELETE FROM identity_refresh_tokens WHERE user_id = $1;

-- name: InsertPasswordReset :exec
INSERT INTO identity_password_resets (id, user_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4);

-- name: GetActivePasswordReset :one
SELECT id, user_id, token_hash, expires_at
FROM identity_password_resets
WHERE token_hash = $1 AND used_at IS NULL;

-- name: MarkPasswordResetUsed :exec
UPDATE identity_password_resets SET used_at = now() WHERE id = $1;

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

-- name: UpdateUserName :one
UPDATE identity_users SET name = $2 WHERE id = $1
RETURNING id, email, password_hash, name, role, created_at;

-- name: ListAddresses :many
SELECT id, user_id, recipient, line1, line2, city, region, postal_code, country, phone, is_default, created_at
FROM identity_addresses
WHERE user_id = $1
ORDER BY is_default DESC, created_at DESC;

-- name: GetAddress :one
SELECT id, user_id, recipient, line1, line2, city, region, postal_code, country, phone, is_default, created_at
FROM identity_addresses
WHERE id = $1 AND user_id = $2;

-- name: CountAddresses :one
SELECT count(*) FROM identity_addresses WHERE user_id = $1;

-- name: InsertAddress :exec
INSERT INTO identity_addresses
    (id, user_id, recipient, line1, line2, city, region, postal_code, country, phone, is_default)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);

-- name: UpdateAddress :exec
UPDATE identity_addresses
SET recipient = $3, line1 = $4, line2 = $5, city = $6, region = $7,
    postal_code = $8, country = $9, phone = $10, is_default = $11
WHERE id = $1 AND user_id = $2;

-- name: DeleteAddress :execrows
DELETE FROM identity_addresses WHERE id = $1 AND user_id = $2;

-- name: ClearDefaultAddresses :exec
UPDATE identity_addresses SET is_default = false WHERE user_id = $1 AND is_default;

-- name: SetAddressDefault :exec
UPDATE identity_addresses SET is_default = true WHERE id = $1 AND user_id = $2;

-- name: NewestAddressID :one
SELECT id FROM identity_addresses WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1;

-- name: UpdateUserRole :exec
UPDATE identity_users SET role = $2 WHERE id = $1;

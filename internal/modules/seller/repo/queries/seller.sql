-- name: InsertApplication :exec
INSERT INTO seller_applications (id, user_id, status, store_name, description, contact, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: GetApplicationByID :one
SELECT id, user_id, status, store_name, description, contact, reject_reason, created_at
FROM seller_applications WHERE id = $1;

-- name: GetActiveApplicationByUser :one
SELECT id, user_id, status, store_name, description, contact, reject_reason, created_at
FROM seller_applications
WHERE user_id = $1 AND status IN ('pending', 'approved', 'suspended');

-- name: GetLatestApplicationByUser :one
SELECT id, user_id, status, store_name, description, contact, reject_reason, created_at
FROM seller_applications WHERE user_id = $1 ORDER BY created_at DESC LIMIT 1;

-- name: UpdateApplicationStatus :exec
UPDATE seller_applications SET status = $2, reject_reason = $3, decided_at = now() WHERE id = $1;

-- name: InsertStore :exec
INSERT INTO seller_stores (id, seller_id, name, logo_url, description, contact)
VALUES ($1, $2, $3, $4, $5, $6);

-- name: GetStoreBySeller :one
SELECT id, seller_id, name, logo_url, description, contact
FROM seller_stores WHERE seller_id = $1;

-- name: UpdateStore :exec
UPDATE seller_stores SET name = $2, logo_url = $3, description = $4, contact = $5, updated_at = now()
WHERE seller_id = $1;

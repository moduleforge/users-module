-- name: CreateUser :one
INSERT INTO users (entity_id, email, email_verified_at, is_admin, auth_issuer, auth_id)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING id, uuid, entity_id, email, email_verified_at, is_admin, default_app_id,
          auth_issuer, auth_id, created_at, updated_at;

-- name: GetUserByID :one
SELECT id, uuid, entity_id, email, email_verified_at, is_admin, default_app_id,
       auth_issuer, auth_id, created_at, updated_at
FROM users
WHERE id = $1;

-- name: GetUserByUUID :one
SELECT id, uuid, entity_id, email, email_verified_at, is_admin, default_app_id,
       auth_issuer, auth_id, created_at, updated_at
FROM users
WHERE uuid = $1;

-- name: GetUserByEmail :one
SELECT id, uuid, entity_id, email, email_verified_at, is_admin, default_app_id,
       auth_issuer, auth_id, created_at, updated_at
FROM users
WHERE lower(email) = lower($1);

-- name: GetUserByAuth :one
SELECT id, uuid, entity_id, email, email_verified_at, is_admin, default_app_id,
       auth_issuer, auth_id, created_at, updated_at
FROM users
WHERE auth_issuer = $1 AND auth_id = $2;

-- name: UpdateUser :exec
UPDATE users
SET email = $2,
    email_verified_at = $3,
    auth_issuer = $4,
    auth_id = $5
WHERE id = $1;

-- name: SetAdmin :exec
UPDATE users
SET is_admin = $2
WHERE id = $1;

-- name: SetDefaultApp :exec
UPDATE users
SET default_app_id = $2
WHERE id = $1;

-- name: SearchUsers :many
SELECT id, uuid, entity_id, email, email_verified_at, is_admin, default_app_id,
       auth_issuer, auth_id, created_at, updated_at
FROM users
WHERE ($1::text IS NULL OR lower(email) LIKE '%' || lower($1::text) || '%')
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;

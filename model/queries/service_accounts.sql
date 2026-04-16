-- name: CreateServiceAccount :one
INSERT INTO service_accounts (entity_id, label)
VALUES ($1, $2)
RETURNING id, entity_id, label, created_at, updated_at;

-- name: GetServiceAccountByEntityID :one
SELECT id, entity_id, label, created_at, updated_at
FROM service_accounts
WHERE entity_id = $1;

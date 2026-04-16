-- name: CreateLegalEntity :one
INSERT INTO legal_entities (entity_id, kind, display_name)
VALUES ($1, $2, $3)
RETURNING id, entity_id, kind, display_name, created_at, updated_at;

-- name: GetLegalEntityByEntityID :one
SELECT id, entity_id, kind, display_name, created_at, updated_at
FROM legal_entities
WHERE entity_id = $1;

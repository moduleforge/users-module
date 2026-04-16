-- name: CreateCorporation :one
INSERT INTO corporations (legal_entity_id, legal_name, jurisdiction)
VALUES ($1, $2, $3)
RETURNING id, legal_entity_id, legal_name, jurisdiction, created_at, updated_at;

-- name: GetCorporationByLegalEntityID :one
SELECT id, legal_entity_id, legal_name, jurisdiction, created_at, updated_at
FROM corporations
WHERE legal_entity_id = $1;

-- name: UpdateCorporation :exec
UPDATE corporations
SET legal_name = $2, jurisdiction = $3
WHERE legal_entity_id = $1;

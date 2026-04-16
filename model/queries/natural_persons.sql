-- name: CreateNaturalPerson :one
INSERT INTO natural_persons (legal_entity_id, given_name, family_name)
VALUES ($1, $2, $3)
RETURNING id, legal_entity_id, given_name, family_name, created_at, updated_at;

-- name: GetNaturalPersonByLegalEntityID :one
SELECT id, legal_entity_id, given_name, family_name, created_at, updated_at
FROM natural_persons
WHERE legal_entity_id = $1;

-- name: UpdateNaturalPerson :exec
UPDATE natural_persons
SET given_name = $2, family_name = $3
WHERE legal_entity_id = $1;

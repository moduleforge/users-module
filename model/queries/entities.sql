-- name: CreateEntity :one
INSERT INTO entities (kind)
VALUES ($1)
RETURNING id, uuid, kind, created_at, updated_at, archived_at;

-- name: GetEntityByUUID :one
SELECT id, uuid, kind, created_at, updated_at, archived_at
FROM entities
WHERE uuid = $1;

-- name: ArchiveEntity :exec
UPDATE entities
SET archived_at = now()
WHERE uuid = $1 AND archived_at IS NULL;

-- name: UnarchiveEntity :exec
UPDATE entities
SET archived_at = NULL
WHERE uuid = $1 AND archived_at IS NOT NULL;

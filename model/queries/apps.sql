-- name: CreateApp :one
INSERT INTO apps (slug, name)
VALUES ($1, $2)
RETURNING id, uuid, slug, name, created_at, updated_at, archived_at;

-- name: GetAppByUUID :one
SELECT id, uuid, slug, name, created_at, updated_at, archived_at
FROM apps
WHERE uuid = $1;

-- name: GetAppBySlug :one
SELECT id, uuid, slug, name, created_at, updated_at, archived_at
FROM apps
WHERE slug = $1;

-- name: ListApps :many
SELECT id, uuid, slug, name, created_at, updated_at, archived_at
FROM apps
WHERE archived_at IS NULL
ORDER BY name;

-- name: UpdateApp :exec
UPDATE apps
SET slug = $2, name = $3
WHERE id = $1;

-- name: ArchiveApp :exec
UPDATE apps
SET archived_at = now()
WHERE id = $1 AND archived_at IS NULL;

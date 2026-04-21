-- name: WriteAudit :exec
INSERT INTO audit_log (actor_user_account_id, assumed_user_account_id, target_entity_id, op, resource, before, after)
VALUES ($1, $2, $3, $4, $5, $6, $7);

-- name: ListAuditByActor :many
SELECT id, actor_user_account_id, assumed_user_account_id, target_entity_id, op, resource, before, after, at
FROM audit_log
WHERE actor_user_account_id = $1
ORDER BY at DESC
LIMIT $2 OFFSET $3;

-- name: ListAuditByTarget :many
SELECT id, actor_user_account_id, assumed_user_account_id, target_entity_id, op, resource, before, after, at
FROM audit_log
WHERE target_entity_id = $1
ORDER BY at DESC
LIMIT $2 OFFSET $3;

-- name: ListRecentAudit :many
SELECT id, actor_user_account_id, assumed_user_account_id, target_entity_id, op, resource, before, after, at
FROM audit_log
ORDER BY at DESC
LIMIT $1 OFFSET $2;

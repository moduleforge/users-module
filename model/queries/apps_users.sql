-- name: AssignUserToApp :exec
INSERT INTO apps_users (app_id, user_id, roles)
VALUES ($1, $2, $3)
ON CONFLICT (app_id, user_id) DO NOTHING;

-- name: RemoveUserFromApp :exec
DELETE FROM apps_users
WHERE app_id = $1 AND user_id = $2;

-- name: ListAppUsers :many
SELECT app_id, user_id, roles, assigned_at
FROM apps_users
WHERE app_id = $1
ORDER BY assigned_at;

-- name: ListUserApps :many
SELECT app_id, user_id, roles, assigned_at
FROM apps_users
WHERE user_id = $1
ORDER BY assigned_at;

-- name: SetAppUserRoles :exec
UPDATE apps_users
SET roles = $3
WHERE app_id = $1 AND user_id = $2;

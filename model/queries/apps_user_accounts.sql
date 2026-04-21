-- name: AssignUserAccountToApp :exec
INSERT INTO apps_user_accounts (app_id, user_account_id, roles)
VALUES ($1, $2, $3)
ON CONFLICT (app_id, user_account_id) DO NOTHING;

-- name: RemoveUserAccountFromApp :exec
DELETE FROM apps_user_accounts
WHERE app_id = $1 AND user_account_id = $2;

-- name: ListAppUserAccounts :many
SELECT app_id, user_account_id, roles, assigned_at
FROM apps_user_accounts
WHERE app_id = $1
ORDER BY assigned_at;

-- name: ListUserAccountApps :many
SELECT app_id, user_account_id, roles, assigned_at
FROM apps_user_accounts
WHERE user_account_id = $1
ORDER BY assigned_at;

-- name: SetAppUserAccountRoles :exec
UPDATE apps_user_accounts
SET roles = $3
WHERE app_id = $1 AND user_account_id = $2;

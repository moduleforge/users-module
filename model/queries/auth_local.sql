-- name: UpsertAuthLocal :exec
INSERT INTO auth_local (user_id, password_hash)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE
SET password_hash = EXCLUDED.password_hash,
    password_updated_at = now();

-- name: GetAuthLocal :one
SELECT user_id, password_hash, password_updated_at, created_at
FROM auth_local
WHERE user_id = $1;

-- name: DeleteAuthLocal :exec
DELETE FROM auth_local
WHERE user_id = $1;

-- name: CreatePasswordReset :one
INSERT INTO password_resets (user_account_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING id, user_account_id, token_hash, expires_at, consumed_at, created_at;

-- name: GetActivePasswordReset :one
SELECT id, user_account_id, token_hash, expires_at, consumed_at, created_at
FROM password_resets
WHERE token_hash = $1
  AND consumed_at IS NULL
  AND expires_at > now();

-- name: ConsumePasswordReset :exec
UPDATE password_resets
SET consumed_at = now()
WHERE id = $1 AND consumed_at IS NULL;

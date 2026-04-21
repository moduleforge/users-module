-- name: CreateEmailCode :one
INSERT INTO email_codes (user_account_id, code_hash, purpose, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING id, user_account_id, code_hash, purpose, expires_at, consumed_at, created_at;

-- name: GetActiveEmailCode :one
SELECT id, user_account_id, code_hash, purpose, expires_at, consumed_at, created_at
FROM email_codes
WHERE user_account_id = $1
  AND purpose = $2
  AND consumed_at IS NULL
  AND expires_at > now()
ORDER BY created_at DESC
LIMIT 1;

-- name: ConsumeEmailCode :exec
UPDATE email_codes
SET consumed_at = now()
WHERE id = $1 AND consumed_at IS NULL;

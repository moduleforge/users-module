-- name: GetOIDCConfig :one
SELECT id, opt_out, setup_token_hash, setup_token_created_at, saved_at
FROM oidc_config
WHERE id = 1;

-- name: UpdateOIDCConfig :exec
-- Persist the operator's opt-out choice (called from POST /v1/oidc-config/confirm).
-- Per-provider enable flags live in the oidc_providers table and are
-- upserted directly — no JSONB column on this singleton since 9.16.
UPDATE oidc_config
SET opt_out = $1,
    saved_at = now()
WHERE id = 1;

-- name: SetSetupTokenHash :exec
-- Install or refresh the active setup-token hash; called once per boot
-- when the state is unconfirmed and no hash is already present.
UPDATE oidc_config
SET setup_token_hash = $1,
    setup_token_created_at = now()
WHERE id = 1;

-- name: ClearSetupTokenHash :exec
-- Clear the setup token once the operator has confirmed configuration.
-- Idempotent — safe to call on every confirmed boot.
UPDATE oidc_config
SET setup_token_hash = NULL,
    setup_token_created_at = NULL
WHERE id = 1;

-- name: GetOIDCProvider :one
-- Fetch one provider override row by id.
SELECT id, display_name, issuer_url, client_id, client_secret,
       claim_style, scopes, enabled, created_at, updated_at
FROM oidc_providers
WHERE id = $1;

-- name: ListOIDCProviders :many
-- Return every provider override row, sorted by id for stable output.
SELECT id, display_name, issuer_url, client_id, client_secret,
       claim_style, scopes, enabled, created_at, updated_at
FROM oidc_providers
ORDER BY id;

-- name: UpsertOIDCProvider :one
-- Insert or replace a provider override row. NULL override fields mean
-- "no opinion at this layer"; the merge code treats NULL as pass-through
-- to env / well-known defaults.
--
-- The handler is responsible for computing the merged row before
-- calling: partial PATCH semantics (e.g. "keep existing client_secret
-- when field is omitted") must be resolved in Go code, because SQL
-- NULL doesn't distinguish "field absent" from "explicit null". This
-- keeps the query trivial and the semantics readable.
INSERT INTO oidc_providers (
    id, display_name, issuer_url, client_id, client_secret,
    claim_style, scopes, enabled
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8
)
ON CONFLICT (id) DO UPDATE SET
    display_name  = EXCLUDED.display_name,
    issuer_url    = EXCLUDED.issuer_url,
    client_id     = EXCLUDED.client_id,
    client_secret = EXCLUDED.client_secret,
    claim_style   = EXCLUDED.claim_style,
    scopes        = EXCLUDED.scopes,
    enabled       = EXCLUDED.enabled
RETURNING id, display_name, issuer_url, client_id, client_secret,
          claim_style, scopes, enabled, created_at, updated_at;

-- name: DeleteOIDCProvider :execrows
-- Remove a provider override row. Used by the "revert" endpoint — after
-- deletion the merge layer falls back to env + well-known defaults.
-- Returns the number of rows deleted so the handler can distinguish
-- 204 (deleted) from 404 (never existed).
DELETE FROM oidc_providers WHERE id = $1;

-- name: SetOIDCProviderEnabled :exec
-- Narrow write used by /v1/oidc-config/confirm when the admin toggles a
-- provider on/off from the summary page. Insert creates a row with just
-- the enabled flag (all other override fields NULL → pass through to env
-- / well-known); update leaves all other columns untouched so the row's
-- existing overrides survive a simple enable/disable.
INSERT INTO oidc_providers (id, enabled)
VALUES ($1, $2)
ON CONFLICT (id) DO UPDATE SET enabled = EXCLUDED.enabled;

-- +goose Up

-- oidc_providers stores per-provider DB overrides for the OIDC provider
-- registry (phase 9.11a). A row here means "the operator edited this
-- provider via the admin GUI"; any NULL column means "no override — use
-- env value if set, otherwise well-known default". The merge layer in
-- api/internal/config/provider_merge.go reads env + this table and
-- produces the effective Provider that the OAuth orchestrator sees.
--
-- Design notes:
--   * id is a slug (lowercase letters / digits / dashes, 2-32 chars,
--     no leading/trailing dash). Matches the env-var naming convention
--     (AUTH_PROVIDER_{ID}_*) once lowercased.
--   * scopes is TEXT[] rather than JSONB because pgx maps it cleanly to
--     []string and we never need partial-path queries into it.
--   * enabled is NOT NULL — a DB row always has an explicit on/off
--     opinion. To "remove the override" the revert endpoint deletes the
--     row entirely, falling back to the pre-existing confirm-flow
--     provider_enabled JSONB in oidc_config.
--   * client_secret is stored plaintext; this matches the env model
--     (AUTH_PROVIDER_*_CLIENT_SECRET is plaintext in .env). The GUI
--     never reads this back — a has_client_secret boolean is surfaced
--     instead.
--   * updated_at is maintained by the shared set_updated_at() trigger
--     defined in 0001_helpers.sql.
CREATE TABLE oidc_providers (
    id TEXT PRIMARY KEY
        CHECK (id ~ '^[a-z][a-z0-9-]{0,30}[a-z0-9]$'),
    display_name   TEXT,
    issuer_url     TEXT,
    client_id      TEXT,
    client_secret  TEXT,
    claim_style    TEXT,
    scopes         TEXT[],
    enabled        BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER oidc_providers_set_updated_at
BEFORE UPDATE ON oidc_providers
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

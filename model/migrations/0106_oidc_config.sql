-- +goose Up

-- oidc_config holds the singleton row that captures the operator's
-- confirmed choices for the OIDC onboarding flow. Only one row ever
-- exists (id = 1, enforced by CHECK); the design choice is deliberate —
-- "current configuration" is a singleton, and keeping the table shape
-- trivial simplifies the upsert + query layer. Per-provider on/off state
-- is owned by oidc_providers.enabled, not this table.
--
-- Columns:
--   opt_out          : persists a "local-auth only" choice made through
--                      the confirm UI. Equivalent in effect to the
--                      NO_OIDC_ACCOUNTS env flag but survives env-var
--                      changes across restarts.
--   setup_token_hash : sha256 hex of the active one-time setup token used
--                      to authorize the /v1/oidc-config/confirm endpoint
--                      when no admin session exists yet. NULL when no
--                      token is active (i.e., state is confirmed).
--   setup_token_created_at : emission timestamp for debugging / auditing.
--   saved_at         : last-saved wall-clock timestamp; powers the GUI
--                      "last saved" label and the revert button.
CREATE TABLE oidc_config (
    id                     INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    opt_out                BOOLEAN NOT NULL DEFAULT FALSE,
    setup_token_hash       TEXT,
    setup_token_created_at TIMESTAMPTZ,
    saved_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed the singleton row so downstream queries can always UPDATE rather
-- than UPSERT. The id = 1 CHECK guarantees subsequent inserts fail loudly.
INSERT INTO oidc_config (id) VALUES (1);

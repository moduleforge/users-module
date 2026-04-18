-- Phase 9.16: unify the per-provider enabled flag on oidc_providers.enabled.
-- oidc_config.provider_enabled was added in 9.9a before per-provider rows
-- existed; now it's a redundant second source of truth that caused the
-- summary page and edit modal to disagree on a provider's enabled state.
-- Dropping it (no prod deploy exists to migrate).
ALTER TABLE oidc_config DROP COLUMN provider_enabled;

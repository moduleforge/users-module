-- user_accounts: an interactive login identity tied to a Legal Entity.
-- account_holder references legal_entities(entity_id), NOT entities(id), because
-- only legal entities (natural_person, corporation) can hold user accounts.
-- Service accounts (machines) cannot hold user accounts — the FK enforces this.
CREATE TABLE user_accounts (
  id                BIGSERIAL PRIMARY KEY,
  uuid              UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  account_holder    BIGINT NOT NULL UNIQUE REFERENCES legal_entities(entity_id) ON DELETE RESTRICT,
  email             TEXT NOT NULL UNIQUE,
  email_verified_at TIMESTAMPTZ,
  is_admin          BOOLEAN NOT NULL DEFAULT FALSE,
  default_app_id    BIGINT, -- FK added in 0104_apps.sql after apps table exists
  auth_issuer       TEXT,
  auth_id           TEXT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Compound partial unique index for OIDC identity lookup.
-- Partial: only enforced when both columns are non-null (local-only accounts have NULL).
CREATE UNIQUE INDEX user_accounts_auth_idx
  ON user_accounts(auth_issuer, auth_id)
  WHERE auth_issuer IS NOT NULL AND auth_id IS NOT NULL;

-- Case-insensitive email lookup for login and search.
CREATE INDEX user_accounts_email_lower_idx ON user_accounts(lower(email));

-- The FK user_accounts.account_holder → legal_entities(entity_id) narrows valid
-- holders to concrete legal entity subtypes (natural_person, corporation).
-- The entities.fundamental_type_id trigger guarantees the type is concrete;
-- the legal_entities FK guarantees the holder is a legal entity, excluding
-- service_accounts at the database level.

CREATE TRIGGER user_accounts_set_updated_at
  BEFORE UPDATE ON user_accounts
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE users (
  id                BIGSERIAL PRIMARY KEY,
  uuid              UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  entity_id         BIGINT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE RESTRICT,
  email             TEXT NOT NULL UNIQUE,
  email_verified_at TIMESTAMPTZ,
  is_admin          BOOLEAN NOT NULL DEFAULT FALSE,
  default_app_id    BIGINT, -- FK added in 0010_apps.sql after apps table exists
  auth_issuer       TEXT,
  auth_id           TEXT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Compound partial unique index for OIDC identity lookup.
-- Partial: only enforced when both columns are non-null (local-only users have NULL).
CREATE UNIQUE INDEX users_auth_idx
  ON users(auth_issuer, auth_id)
  WHERE auth_issuer IS NOT NULL AND auth_id IS NOT NULL;

-- Case-insensitive email lookup for login and search.
CREATE INDEX users_email_lower_idx ON users(lower(email));

-- Trigger: enforce that entity_id references a leaf entity kind.
-- Leaf kinds are: natural_person, corporation (via legal_entities), or service_account.
-- Postgres CHECK constraints cannot cross tables, so we use a trigger instead.
CREATE OR REPLACE FUNCTION users_enforce_leaf_entity() RETURNS TRIGGER AS $$
DECLARE
  v_entity_kind TEXT;
  v_legal_kind  TEXT;
BEGIN
  SELECT kind INTO v_entity_kind FROM entities WHERE id = NEW.entity_id;
  IF v_entity_kind IS NULL THEN
    RAISE EXCEPTION 'users.entity_id % does not exist', NEW.entity_id;
  END IF;

  IF v_entity_kind = 'service_account' THEN
    -- service_account is itself a leaf
    RETURN NEW;
  END IF;

  IF v_entity_kind = 'legal_entity' THEN
    SELECT kind INTO v_legal_kind FROM legal_entities WHERE entity_id = NEW.entity_id;
    IF v_legal_kind IN ('natural_person', 'corporation') THEN
      RETURN NEW;
    END IF;
    RAISE EXCEPTION 'users.entity_id % is a legal_entity but not a leaf kind (got %)', NEW.entity_id, v_legal_kind;
  END IF;

  RAISE EXCEPTION 'users.entity_id % has unknown kind %', NEW.entity_id, v_entity_kind;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_enforce_leaf_entity_trg
  BEFORE INSERT OR UPDATE OF entity_id ON users
  FOR EACH ROW EXECUTE FUNCTION users_enforce_leaf_entity();

CREATE TRIGGER users_set_updated_at
  BEFORE UPDATE ON users
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

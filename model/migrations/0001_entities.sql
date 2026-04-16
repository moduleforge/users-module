-- pgcrypto is the single Postgres-specific dependency; provides gen_random_uuid().
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE entities (
  id           BIGSERIAL PRIMARY KEY,
  uuid         UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  kind         TEXT NOT NULL CHECK (kind IN ('legal_entity', 'service_account')),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at  TIMESTAMPTZ
);

CREATE INDEX entities_kind_idx ON entities(kind);
CREATE INDEX entities_archived_at_idx ON entities(archived_at) WHERE archived_at IS NOT NULL;

CREATE TRIGGER entities_set_updated_at
  BEFORE UPDATE ON entities
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

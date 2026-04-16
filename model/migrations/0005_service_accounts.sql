CREATE TABLE service_accounts (
  id          BIGSERIAL PRIMARY KEY,
  entity_id   BIGINT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE RESTRICT,
  label       TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER service_accounts_set_updated_at
  BEFORE UPDATE ON service_accounts
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

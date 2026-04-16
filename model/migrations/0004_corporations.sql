CREATE TABLE corporations (
  id              BIGSERIAL PRIMARY KEY,
  legal_entity_id BIGINT NOT NULL UNIQUE REFERENCES legal_entities(id) ON DELETE RESTRICT,
  legal_name      TEXT NOT NULL,
  jurisdiction    TEXT,
  -- legal_id intentionally omitted in v1
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER corporations_set_updated_at
  BEFORE UPDATE ON corporations
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

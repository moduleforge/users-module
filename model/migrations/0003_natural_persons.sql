CREATE TABLE natural_persons (
  id              BIGSERIAL PRIMARY KEY,
  legal_entity_id BIGINT NOT NULL UNIQUE REFERENCES legal_entities(id) ON DELETE RESTRICT,
  given_name      TEXT,
  family_name     TEXT,
  -- legal_id intentionally omitted in v1 (PII)
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER natural_persons_set_updated_at
  BEFORE UPDATE ON natural_persons
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

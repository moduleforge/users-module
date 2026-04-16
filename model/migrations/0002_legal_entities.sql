CREATE TABLE legal_entities (
  id           BIGSERIAL PRIMARY KEY,
  entity_id    BIGINT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE RESTRICT,
  kind         TEXT NOT NULL CHECK (kind IN ('natural_person', 'corporation')),
  display_name TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX legal_entities_kind_idx ON legal_entities(kind);

CREATE TRIGGER legal_entities_set_updated_at
  BEFORE UPDATE ON legal_entities
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE audit_log (
  id                BIGSERIAL PRIMARY KEY,
  actor_user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  assumed_user_id   BIGINT REFERENCES users(id) ON DELETE RESTRICT,
  target_entity_id  BIGINT REFERENCES entities(id) ON DELETE SET NULL,
  op                TEXT NOT NULL CHECK (op IN ('create', 'update', 'delete', 'assume', 'login', 'grant', 'revoke')),
  resource          TEXT NOT NULL,
  before            JSONB,
  after             JSONB,
  at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Per-actor activity log (recent first).
CREATE INDEX audit_log_actor_at_idx ON audit_log(actor_user_id, at DESC);

-- Per-target change history (recent first); partial since target can be NULL (e.g., login events).
CREATE INDEX audit_log_target_at_idx ON audit_log(target_entity_id, at DESC)
  WHERE target_entity_id IS NOT NULL;

-- Filter by operation type.
CREATE INDEX audit_log_op_at_idx ON audit_log(op, at DESC);

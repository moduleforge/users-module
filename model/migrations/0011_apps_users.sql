CREATE TABLE apps_users (
  app_id      BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  roles       TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (app_id, user_id)
);

-- Index for "list all apps for a given user" queries.
CREATE INDEX apps_users_user_idx ON apps_users(user_id);

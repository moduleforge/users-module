CREATE TABLE apps_user_accounts (
  app_id          BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  user_account_id BIGINT NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
  roles           TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
  assigned_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (app_id, user_account_id)
);

-- Index for "list all apps for a given user account" queries.
CREATE INDEX apps_user_accounts_user_account_idx ON apps_user_accounts(user_account_id);

CREATE TABLE auth_local (
  user_id             BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  password_hash       TEXT NOT NULL,            -- argon2id encoded string
  password_updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

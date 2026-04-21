CREATE TABLE password_resets (
  id              BIGSERIAL PRIMARY KEY,
  user_account_id BIGINT NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
  token_hash      TEXT NOT NULL UNIQUE,              -- sha256 of opaque token
  expires_at      TIMESTAMPTZ NOT NULL,
  consumed_at     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

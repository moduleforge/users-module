CREATE TABLE email_codes (
  id              BIGSERIAL PRIMARY KEY,
  user_account_id BIGINT NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
  code_hash       TEXT NOT NULL,                     -- sha256(salt+code) or bcrypt
  purpose         TEXT NOT NULL CHECK (purpose IN ('login', 'verify_email')),
  expires_at      TIMESTAMPTZ NOT NULL,
  consumed_at     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Index for looking up active (unconsumed) codes per user account and purpose.
CREATE INDEX email_codes_user_account_purpose_idx
  ON email_codes(user_account_id, purpose)
  WHERE consumed_at IS NULL;

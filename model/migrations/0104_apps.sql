CREATE TABLE apps (
  id          BIGSERIAL PRIMARY KEY,
  uuid        UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  slug        TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at TIMESTAMPTZ
);

CREATE TRIGGER apps_set_updated_at
  BEFORE UPDATE ON apps
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Now that the apps table exists, add the deferred FK from user_accounts.default_app_id.
ALTER TABLE user_accounts
  ADD CONSTRAINT user_accounts_default_app_fk
  FOREIGN KEY (default_app_id) REFERENCES apps(id) ON DELETE SET NULL;

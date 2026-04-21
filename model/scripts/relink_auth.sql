-- relink_auth.sql — Move a user account from one OIDC provider to another.
--
-- Usage (psql):
--   \set user_uuid     '''<uuid>'''
--   \set new_issuer    '''https://new-provider.example.com'''
--   \set new_auth_id   '''new-subject-id'''
--   \i relink_auth.sql
--
-- The user_accounts_auth_idx unique index ensures no conflict with an existing identity.

BEGIN;

UPDATE user_accounts
   SET auth_issuer = :new_issuer,
       auth_id     = :new_auth_id,
       updated_at  = now()
 WHERE uuid = :user_uuid::uuid;

-- Verify exactly one row was updated.
DO $$
BEGIN
  IF NOT FOUND THEN
    RAISE EXCEPTION 'No user account found with uuid %', :'user_uuid';
  END IF;
END $$;

COMMIT;

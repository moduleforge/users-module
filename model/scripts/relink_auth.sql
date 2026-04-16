-- relink_auth.sql — Move a user from one OIDC provider to another.
--
-- Usage (psql):
--   \set user_uuid     '''<uuid>'''
--   \set new_issuer    '''https://new-provider.example.com'''
--   \set new_auth_id   '''new-subject-id'''
--   \i relink_auth.sql
--
-- The users_auth_idx unique index ensures no conflict with an existing identity.

BEGIN;

UPDATE users
   SET auth_issuer = :new_issuer,
       auth_id     = :new_auth_id,
       updated_at  = now()
 WHERE uuid = :user_uuid::uuid;

-- Verify exactly one row was updated.
DO $$
BEGIN
  IF NOT FOUND THEN
    RAISE EXCEPTION 'No user found with uuid %', :'user_uuid';
  END IF;
END $$;

COMMIT;

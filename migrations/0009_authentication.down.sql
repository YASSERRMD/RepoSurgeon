-- rs:critical=true
-- Reverse of 0009_authentication.
ALTER TABLE memberships DROP CONSTRAINT IF EXISTS memberships_role_check;
ALTER TABLE memberships ADD CONSTRAINT memberships_role_check
    CHECK (role IN ('owner', 'admin', 'member', 'viewer'));

ALTER TABLE credential_metadata
    DROP COLUMN IF EXISTS last_verified_at,
    DROP COLUMN IF EXISTS status,
    DROP COLUMN IF EXISTS key_version,
    DROP COLUMN IF EXISTS nonce,
    DROP COLUMN IF EXISTS ciphertext;

DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS permissions;
DROP TABLE IF EXISTS login_attempts;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS auth_identities;

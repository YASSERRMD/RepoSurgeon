-- rs:critical=true
-- M02 authentication and authorization:
--   * local (and future OIDC) identities with hashed passwords
--   * server-side sessions with refresh rotation and revocation
--   * login attempt ledger for brute-force controls
--   * data-driven role permissions (owner/admin/maintainer/reviewer/member/viewer)
--   * encrypted provider credentials with key-version metadata
--
-- Roles are additive: M01 shipped owner/admin/member/viewer; the check
-- constraint is replaced here to add maintainer and reviewer.

CREATE TABLE auth_identities (
    id             text PRIMARY KEY,
    user_id        text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind           text NOT NULL CHECK (kind IN ('local', 'oidc')),
    subject        text NOT NULL,
    password_hash  text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kind, subject)
);

CREATE INDEX auth_identities_user_idx ON auth_identities (user_id);

CREATE TABLE sessions (
    id           text PRIMARY KEY,
    user_id      text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz,
    revoked_reason text,
    rotated_from text REFERENCES sessions (id) ON DELETE SET NULL,
    ip_hash      text,
    user_agent   text,
    csrf_hash    text NOT NULL
);

CREATE INDEX sessions_user_active_idx ON sessions (user_id, expires_at DESC);

CREATE TABLE login_attempts (
    id               bigserial PRIMARY KEY,
    email_normalized text NOT NULL,
    attempted_at     timestamptz NOT NULL DEFAULT now(),
    succeeded        boolean NOT NULL
);

CREATE INDEX login_attempts_email_time_idx ON login_attempts (email_normalized, attempted_at DESC);

CREATE TABLE permissions (
    key         text PRIMARY KEY,
    description text NOT NULL
);

CREATE TABLE role_permissions (
    role           text NOT NULL,
    permission_key text NOT NULL REFERENCES permissions (key) ON DELETE CASCADE,
    PRIMARY KEY (role, permission_key)
);

INSERT INTO permissions (key, description) VALUES
    ('project.read', 'Read projects, issues and run history'),
    ('project.write', 'Create and update projects'),
    ('run.execute', 'Start and drive agent runs'),
    ('credential.admin', 'Create, rotate and revoke provider credentials'),
    ('member.admin', 'Invite, update and remove members'),
    ('approval.decide', 'Approve or reject pending approvals'),
    ('merge.execute', 'Merge pull requests into protected branches');

INSERT INTO role_permissions (role, permission_key) VALUES
    ('owner', 'project.read'), ('owner', 'project.write'), ('owner', 'run.execute'),
    ('owner', 'credential.admin'), ('owner', 'member.admin'), ('owner', 'approval.decide'),
    ('owner', 'merge.execute'),
    ('admin', 'project.read'), ('admin', 'project.write'), ('admin', 'run.execute'),
    ('admin', 'credential.admin'), ('admin', 'member.admin'), ('admin', 'approval.decide'),
    ('maintainer', 'project.read'), ('maintainer', 'project.write'), ('maintainer', 'run.execute'),
    ('maintainer', 'approval.decide'),
    ('reviewer', 'project.read'), ('reviewer', 'approval.decide'),
    ('member', 'project.read'), ('member', 'project.write'),
    ('viewer', 'project.read');

-- Provider credentials: encrypted blobs plus key-version metadata. The plain
-- value is never stored, logged or returned by any API.
ALTER TABLE credential_metadata
    ADD COLUMN ciphertext     bytea,
    ADD COLUMN nonce          bytea,
    ADD COLUMN key_version    integer,
    ADD COLUMN status         text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'rotating', 'revoked', 'failed')),
    ADD COLUMN last_verified_at timestamptz;

ALTER TABLE memberships DROP CONSTRAINT IF EXISTS memberships_role_check;
ALTER TABLE memberships ADD CONSTRAINT memberships_role_check
    CHECK (role IN ('owner', 'admin', 'maintainer', 'reviewer', 'member', 'viewer'));

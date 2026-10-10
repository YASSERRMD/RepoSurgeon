-- rs:critical=true
-- M01 identity: users, organizations and memberships.
-- Every tenant-scoped table carries an opaque text id plus created/updated
-- timestamps; soft deletion uses deleted_at.

CREATE TABLE organizations (
    id           text PRIMARY KEY,
    slug         text NOT NULL UNIQUE,
    name         text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    deleted_at   timestamptz
);

CREATE INDEX organizations_active_idx ON organizations (slug) WHERE deleted_at IS NULL;

CREATE TABLE users (
    id            text PRIMARY KEY,
    email         text NOT NULL,
    display_name  text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    deleted_at    timestamptz
);

-- Case-insensitive uniqueness without requiring the citext extension.
CREATE UNIQUE INDEX users_email_unique_idx ON users (lower(email)) WHERE deleted_at IS NULL;

CREATE TABLE memberships (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id          text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role             text NOT NULL CHECK (role IN ('owner', 'admin', 'member', 'viewer')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, user_id)
);

CREATE INDEX memberships_user_idx ON memberships (user_id);

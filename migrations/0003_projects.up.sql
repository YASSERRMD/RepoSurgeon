-- rs:critical=true
-- M01 projects: a project belongs to exactly one organization and carries
-- JSONB settings plus an optimistic-concurrency version.
CREATE TABLE projects (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name             text NOT NULL,
    description      text NOT NULL DEFAULT '',
    settings         jsonb NOT NULL DEFAULT '{}'::jsonb,
    version          bigint NOT NULL DEFAULT 0,
    created_by       text REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz
);

-- A live project name is unique within its organization.
CREATE UNIQUE INDEX projects_org_name_active_idx
    ON projects (organization_id, name)
    WHERE deleted_at IS NULL;

CREATE INDEX projects_org_created_idx ON projects (organization_id, created_at DESC);

CREATE TABLE project_settings_revisions (
    id            text PRIMARY KEY,
    project_id    text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    version       bigint NOT NULL,
    settings      jsonb NOT NULL,
    changed_by    text REFERENCES users (id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, version)
);

-- rs:critical=true
-- M01 repository connections, imported issues and tasks.
CREATE TABLE repository_connections (
    id                text PRIMARY KEY,
    organization_id   text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id        text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    installation_id   text NOT NULL,
    repo_full_name    text NOT NULL,
    default_branch    text NOT NULL DEFAULT 'main',
    is_private        boolean NOT NULL DEFAULT true,
    created_by        text REFERENCES users (id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    deleted_at        timestamptz
);

CREATE UNIQUE INDEX repository_connections_project_repo_active_idx
    ON repository_connections (project_id, repo_full_name)
    WHERE deleted_at IS NULL;

CREATE TABLE issues (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id       text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    repository_id    text REFERENCES repository_connections (id) ON DELETE SET NULL,
    external_id      text,
    number           bigint,
    title            text NOT NULL,
    body             text NOT NULL DEFAULT '',
    state            text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'closed')),
    body_hash        text NOT NULL,
    last_synced_at   timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX issues_repo_external_idx
    ON issues (repository_id, external_id)
    WHERE repository_id IS NOT NULL AND external_id IS NOT NULL;

CREATE INDEX issues_project_created_idx ON issues (project_id, created_at DESC);

CREATE TABLE tasks (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id       text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    issue_id         text NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    title            text NOT NULL,
    status           text NOT NULL DEFAULT 'pending'
                         CHECK (status IN ('pending', 'in_progress', 'completed', 'cancelled')),
    created_by       text REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX tasks_project_idx ON tasks (project_id, created_at DESC);

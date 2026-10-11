-- rs:critical=true
-- M08 repository index: files, symbols, provenance and context manifests.
-- Index rows are derived data: they are rebuilt from a workspace checkout and
-- never hand-edited.

CREATE TABLE repo_files (
    id               text PRIMARY KEY,
    workspace_id     text NOT NULL,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    path             text NOT NULL,
    language         text NOT NULL,
    size_bytes       bigint NOT NULL,
    sha256           text NOT NULL,
    line_count       integer NOT NULL DEFAULT 0,
    indexed_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, path)
);

CREATE INDEX repo_files_workspace_idx ON repo_files (workspace_id, language);
CREATE INDEX repo_files_hash_idx ON repo_files (workspace_id, sha256);

CREATE TABLE repo_symbols (
    id               text PRIMARY KEY,
    workspace_id     text NOT NULL,
    path             text NOT NULL,
    name             text NOT NULL,
    kind             text NOT NULL
                         CHECK (kind IN ('func', 'type', 'const', 'var', 'class', 'def', 'interface', 'other')),
    line             integer NOT NULL,
    signature        text NOT NULL DEFAULT '',
    exported         boolean NOT NULL DEFAULT false,
    UNIQUE (workspace_id, path, name, kind, line)
);

CREATE INDEX repo_symbols_name_idx ON repo_symbols (workspace_id, name);

CREATE TABLE context_manifests (
    id               text PRIMARY KEY,
    run_id           text REFERENCES runs (id) ON DELETE CASCADE,
    workspace_id     text NOT NULL,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id       text REFERENCES projects (id) ON DELETE CASCADE,
    query            text NOT NULL DEFAULT '',
    strategy         text NOT NULL,
    token_budget     integer NOT NULL,
    token_estimate   integer NOT NULL,
    truncated        boolean NOT NULL DEFAULT false,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX context_manifests_run_idx ON context_manifests (run_id, created_at DESC);

CREATE TABLE context_provenance (
    id               text PRIMARY KEY,
    manifest_id      text NOT NULL REFERENCES context_manifests (id) ON DELETE CASCADE,
    path             text NOT NULL,
    line_start       integer NOT NULL,
    line_end         integer NOT NULL,
    reason           text NOT NULL,
    token_estimate   integer NOT NULL,
    byte_sha256      text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX context_provenance_manifest_idx ON context_provenance (manifest_id);

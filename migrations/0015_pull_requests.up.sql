-- rs:critical=true
-- M13 issue-to-PR lifecycle: the git commit metadata RepoSurgeon composed, the
-- pull request it opened, the real GitHub check runs and reviews tracked for
-- it, the human-authorized merge action, and the resulting merge SHA plus
-- branch-deletion status. Every row cascades with the run so a deleted project
-- cannot leave a merge record behind.

CREATE TABLE pull_requests (
    id             text PRIMARY KEY,
    run_id         text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    organization_id text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    workspace_id   text NOT NULL DEFAULT '',
    number          bigint NOT NULL DEFAULT 0,
    url             text NOT NULL DEFAULT '',
    repo_owner      text NOT NULL DEFAULT '',
    repo_name       text NOT NULL DEFAULT '',
    head_branch     text NOT NULL DEFAULT '',
    base_branch     text NOT NULL DEFAULT '',
    title           text NOT NULL DEFAULT '',
    body            text NOT NULL DEFAULT '',
    issue_number    bigint NOT NULL DEFAULT 0,
    head_sha        text NOT NULL DEFAULT '',
    base_sha        text NOT NULL DEFAULT '',
    state           text NOT NULL
                   CHECK (state IN ('open', 'merged', 'closed', 'blocked', 'abandoned')),
    checks          jsonb NOT NULL DEFAULT '[]'::jsonb,
    reviews         jsonb NOT NULL DEFAULT '[]'::jsonb,
    approval_count integer NOT NULL DEFAULT 0,
    merge_state     text NOT NULL DEFAULT '',
    blocked_reason text NOT NULL DEFAULT '',
    merge_sha       text NOT NULL DEFAULT '',
    branch_deleted  boolean NOT NULL DEFAULT false,
    effect_id       text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, effect_id)
);

CREATE INDEX pull_requests_run_idx ON pull_requests (run_id, created_at DESC);
CREATE INDEX pull_requests_number_idx ON pull_requests (organization_id, repo_owner, repo_name, number);

CREATE TABLE pr_commits (
    id             text PRIMARY KEY,
    pull_request_id text NOT NULL REFERENCES pull_requests (id) ON DELETE CASCADE,
    organization_id text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    run_id          text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    sha             text NOT NULL,
    message         text NOT NULL DEFAULT '',
    author_name     text NOT NULL DEFAULT '',
    author_email    text NOT NULL DEFAULT '',
    committer_name  text NOT NULL DEFAULT '',
    committer_email text NOT NULL DEFAULT '',
    files           text[] NOT NULL DEFAULT '{}',
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (pull_request_id, sha)
);

CREATE INDEX pr_commits_pr_idx ON pr_commits (pull_request_id, created_at);

CREATE TABLE merge_actions (
    id              text PRIMARY KEY,
    run_id          text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    organization_id text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    pull_request_id text NOT NULL DEFAULT '',
    authorized_by   text NOT NULL,
    method          text NOT NULL DEFAULT 'merge',
    merge_sha       text NOT NULL DEFAULT '',
    succeeded       boolean NOT NULL DEFAULT false,
    outcome         text NOT NULL DEFAULT '',
    effect_id       text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, effect_id)
);

CREATE INDEX merge_actions_run_idx ON merge_actions (run_id, created_at);

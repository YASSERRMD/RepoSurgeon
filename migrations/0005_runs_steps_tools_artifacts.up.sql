-- rs:critical=true
-- M01 agent execution core: runs, run steps, tool calls and artifacts.
-- State values are enforced by the Go state machine; the constraint here is a
-- backstop.
CREATE TABLE runs (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id       text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    task_id          text REFERENCES tasks (id) ON DELETE SET NULL,
    issue_id         text REFERENCES issues (id) ON DELETE SET NULL,
    state            text NOT NULL
                         CHECK (state IN ('queued', 'intaking', 'analyzing', 'planning',
                                          'awaiting_approval', 'editing', 'testing',
                                          'reviewing', 'pr_ready', 'pr_open',
                                          'completed', 'failed', 'cancelled', 'blocked')),
    base_sha         text,
    model_config     jsonb NOT NULL DEFAULT '{}'::jsonb,
    limits           jsonb NOT NULL DEFAULT '{}'::jsonb,
    policy           jsonb NOT NULL DEFAULT '{}'::jsonb,
    attempt          integer NOT NULL DEFAULT 1,
    created_by       text REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    started_at       timestamptz,
    finished_at      timestamptz
);

CREATE INDEX runs_project_state_idx ON runs (project_id, state, created_at DESC);

CREATE TABLE run_steps (
    id            text PRIMARY KEY,
    run_id        text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    organization_id text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id    text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    type          text NOT NULL,
    status        text NOT NULL CHECK (status IN ('pending', 'running', 'succeeded', 'failed', 'skipped')),
    attempt       integer NOT NULL DEFAULT 1,
    input_ref     text,
    output_ref    text,
    error         text,
    duration_ms   bigint,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX run_steps_run_idx ON run_steps (run_id, created_at);

CREATE TABLE tool_calls (
    id               text PRIMARY KEY,
    run_id           text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    run_step_id      text REFERENCES run_steps (id) ON DELETE SET NULL,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id       text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    effect_id        text NOT NULL,
    tool             text NOT NULL,
    risk_class       text NOT NULL CHECK (risk_class IN ('read_only', 'workspace_write', 'sandboxed_exec', 'external_side_effect')),
    arguments_hash   text NOT NULL,
    result_status    text,
    exit_code        integer,
    duration_ms      bigint,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, effect_id)
);

CREATE INDEX tool_calls_run_idx ON tool_calls (run_id, created_at);

CREATE TABLE artifacts (
    id               text PRIMARY KEY,
    run_id           text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id       text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('diff', 'log', 'test_report', 'screenshot', 'export', 'other')),
    uri              text NOT NULL,
    sha256           text,
    size_bytes       bigint,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX artifacts_run_idx ON artifacts (run_id, created_at);

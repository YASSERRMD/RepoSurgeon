-- rs:critical=true
-- M09 typed plans: versions, DAG nodes, acceptance criteria, verification
-- findings and plan approvals. Plans are proposals; only approval records and
-- deterministic verification gates carry authority.

CREATE TABLE plans (
    id               text PRIMARY KEY,
    run_id           text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id       text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    revision         integer NOT NULL DEFAULT 1,
    kind             text NOT NULL
                         CHECK (kind IN ('bug', 'feature', 'refactor', 'security')),
    risk             text NOT NULL
                         CHECK (risk IN ('low', 'medium', 'high')),
    summary          text NOT NULL,
    base_sha         text NOT NULL,
    context_sha256   text NOT NULL,
    status           text NOT NULL
                         CHECK (status IN ('draft', 'awaiting_approval', 'approved', 'rejected', 'superseded')),
    rejected_reason  text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, revision)
);

CREATE INDEX plans_run_idx ON plans (run_id, revision DESC);

CREATE TABLE plan_nodes (
    id               text PRIMARY KEY,
    plan_id          text NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    node_key         text NOT NULL,
    title            text NOT NULL,
    kind             text NOT NULL
                         CHECK (kind IN ('investigate', 'edit', 'test', 'verify', 'review')),
    path             text NOT NULL DEFAULT '',
    line_hint        integer NOT NULL DEFAULT 0,
    prerequisites    text[] NOT NULL DEFAULT '{}',
    evidence         jsonb NOT NULL DEFAULT '[]'::jsonb,
    position         integer NOT NULL DEFAULT 0,
    UNIQUE (plan_id, node_key)
);

CREATE INDEX plan_nodes_plan_idx ON plan_nodes (plan_id, position);

CREATE TABLE plan_acceptance (
    id               text PRIMARY KEY,
    plan_id          text NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    criteria_key     text NOT NULL,
    description      text NOT NULL,
    verification     text NOT NULL DEFAULT '',
    satisfied        boolean NOT NULL DEFAULT false,
    UNIQUE (plan_id, criteria_key)
);

CREATE TABLE plan_findings (
    id               text PRIMARY KEY,
    plan_id          text NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    severity         text NOT NULL CHECK (severity IN ('error', 'warning')),
    code             text NOT NULL,
    message          text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX plan_findings_plan_idx ON plan_findings (plan_id, created_at);

CREATE TABLE plan_approvals (
    id               text PRIMARY KEY,
    plan_id          text NOT NULL REFERENCES plans (id) ON DELETE CASCADE,
    revision         integer NOT NULL,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    approved_by      text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    decision         text NOT NULL CHECK (decision IN ('approved', 'rejected')),
    reason           text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (plan_id, revision, approved_by)
);

CREATE INDEX plan_approvals_plan_idx ON plan_approvals (plan_id, revision DESC);

-- A superseded revision must invalidate approvals: the trigger records the
-- invalidation so the approval timeline stays append-only.
CREATE TABLE plan_approval_invalidations (
    id               text PRIMARY KEY,
    plan_id          text NOT NULL,
    revision         integer NOT NULL,
    reason           text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);

-- rs:critical=true
-- M06 durable task queue: leases, heartbeats and attempts. Workers claim
-- rows with FOR UPDATE SKIP LOCKED so several workers never run the same job.

CREATE TABLE task_queue (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id       text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    run_id           text REFERENCES runs (id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('run', 'sync', 'probe')),
    status           text NOT NULL
                         CHECK (status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    payload          jsonb NOT NULL DEFAULT '{}'::jsonb,
    attempt          integer NOT NULL DEFAULT 0,
    max_attempts     integer NOT NULL DEFAULT 5,
    available_at     timestamptz NOT NULL DEFAULT now(),
    lease_owner      text,
    lease_expires_at timestamptz,
    last_heartbeat   timestamptz,
    last_error       text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX task_queue_claim_idx ON task_queue (status, available_at)
    WHERE status = 'pending';

CREATE INDEX task_queue_lease_idx ON task_queue (lease_expires_at)
    WHERE status = 'running';

CREATE INDEX task_queue_project_idx ON task_queue (project_id, created_at DESC);

-- Runs gain their first durable step markers; steps are appended as work
-- proceeds and never rewritten.
CREATE TABLE IF NOT EXISTS run_state_history (
    id           text PRIMARY KEY,
    run_id       text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    from_state   text,
    to_state     text NOT NULL,
    reason       text NOT NULL DEFAULT '',
    actor_type   text NOT NULL DEFAULT 'system'
                 CHECK (actor_type IN ('system', 'user', 'worker', 'model')),
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX run_state_history_run_idx ON run_state_history (run_id, created_at);

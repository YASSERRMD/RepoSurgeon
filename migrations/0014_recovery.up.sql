-- rs:critical=true
-- M12 iterative debugging and state reconciliation: the state recovery
-- journal, bounded attempt history, per-class attempt budgets, durable effect
-- receipts and content-bearing checkpoints.
--
-- Every table is run scoped and cascades with the run so a deleted project
-- cannot leave recovery evidence behind.

CREATE TABLE recovery_events (
    id              text PRIMARY KEY,
    run_id          text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    organization_id text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    attempt         integer NOT NULL DEFAULT 0,
    class           text NOT NULL DEFAULT 'unknown',
    kind            text NOT NULL
                    CHECK (kind IN ('retry_scheduled', 'retry_exhausted', 'loop_detected',
                                    'blocked', 'recovered', 'rollback', 'lease_recovered',
                                    'receipt_replayed', 'credential_refreshed')),
    reason          text NOT NULL DEFAULT '',
    evidence        jsonb NOT NULL DEFAULT '{}'::jsonb,
    actor_type      text NOT NULL DEFAULT 'system',
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX recovery_events_run_idx ON recovery_events (run_id, created_at);

CREATE TABLE recovery_attempts (
    id                text PRIMARY KEY,
    run_id            text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    organization_id   text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    attempt_number    integer NOT NULL,
    kind              text NOT NULL
                      CHECK (kind IN ('patched', 'noop', 'blocked')),
    fingerprint       text NOT NULL DEFAULT '',
    patch_fingerprint text NOT NULL DEFAULT '',
    failing_count     integer NOT NULL DEFAULT 0,
    verdict           text NOT NULL DEFAULT '',
    note              text NOT NULL DEFAULT '',
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, attempt_number)
);

CREATE INDEX recovery_attempts_run_idx ON recovery_attempts (run_id, attempt_number DESC);

CREATE TABLE recovery_budgets (
    run_id          text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    class           text NOT NULL,
    allowed         integer NOT NULL DEFAULT 0,
    used            integer NOT NULL DEFAULT 0,
    remaining       integer NOT NULL DEFAULT 0,
    exhausted       boolean NOT NULL DEFAULT false,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, class)
);

CREATE TABLE effect_receipts (
    id          text PRIMARY KEY,
    run_id      text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    operation   text NOT NULL,
    effect_id   text NOT NULL,
    status      text NOT NULL,
    response    jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (run_id, effect_id)
);

CREATE INDEX effect_receipts_run_idx ON effect_receipts (run_id, created_at);

CREATE TABLE checkpoints (
    id            text PRIMARY KEY,
    run_id        text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    organization_id text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    workspace_id  text NOT NULL,
    verdict       text NOT NULL DEFAULT '',
    files         jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX checkpoints_run_idx ON checkpoints (run_id, created_at DESC);

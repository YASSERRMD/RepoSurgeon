-- rs:critical=true
-- M15: realtime timeline, auditability, cost and observability.
-- Append-only run events: every state change, step, tool call, test, approval,
-- PR action and recovery is recorded first, then streamed to clients over SSE.
CREATE TABLE run_events (
    id              text PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id      text REFERENCES projects (id) ON DELETE CASCADE,
    run_id          text REFERENCES runs (id) ON DELETE CASCADE,
    actor_id        text REFERENCES users (id) ON DELETE SET NULL,
    causation_id    text NOT NULL,             -- the upstream event/action that caused this one
    sequence        bigserial NOT NULL,        -- total order within an organization/run
    type            text NOT NULL,             -- run.created, run.started, step.began, tool.called, ...
    step            text,                      -- the run step this event belongs to
    payload         jsonb NOT NULL DEFAULT '{}'::jsonb,  -- redacted before insert
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX run_events_run_seq_idx ON run_events (run_id, sequence DESC);
CREATE INDEX run_events_org_seq_idx ON run_events (organization_id, sequence DESC);
CREATE INDEX run_events_causation_idx ON run_events (causation_id);

-- Guard against accidental updates and deletes of event rows (append-only).
CREATE OR REPLACE FUNCTION run_events_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'run_events rows are append-only (attempted %)', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER run_events_no_update
    BEFORE UPDATE OR DELETE ON run_events
    FOR EACH ROW EXECUTE FUNCTION run_events_immutable();

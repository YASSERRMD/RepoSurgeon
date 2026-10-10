-- rs:critical=true
-- M01 append-only audit trail. Rows are inserted, never updated; every
-- mutating request writes one row carrying its correlation ID. Payloads are
-- redacted before insertion by internal/redact.
CREATE TABLE audit_events (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id       text REFERENCES projects (id) ON DELETE CASCADE,
    run_id           text REFERENCES runs (id) ON DELETE CASCADE,
    actor_id         text REFERENCES users (id) ON DELETE SET NULL,
    action           text NOT NULL,
    entity_type      text NOT NULL,
    entity_id        text,
    request_id       text NOT NULL,
    payload          jsonb NOT NULL DEFAULT '{}'::jsonb,
    sequence         bigserial NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_events_project_seq_idx ON audit_events (project_id, sequence DESC);
CREATE INDEX audit_events_org_seq_idx ON audit_events (organization_id, sequence DESC);
CREATE INDEX audit_events_request_idx ON audit_events (request_id);

-- Guard against accidental updates and deletes of audit rows.
CREATE OR REPLACE FUNCTION audit_events_immutable() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'audit_events rows are append-only (attempted %)', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_events_no_update
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_immutable();

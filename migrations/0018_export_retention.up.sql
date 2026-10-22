-- rs:critical=true
-- M15: export metadata and retention policies.
-- Export records track requested trace exports so clients can poll for a
-- ready document; retention policies govern how long events and usage rows
-- are kept before being purged.
CREATE TABLE export_records (
    id              text PRIMARY KEY,
    organization_id text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    run_id          text REFERENCES runs (id) ON DELETE CASCADE,
    format          text NOT NULL CHECK (format IN ('json', 'markdown')),
    status          text NOT NULL CHECK (status IN ('pending', 'ready', 'failed')),
    expires_at      timestamptz NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX export_records_run_idx ON export_records (run_id);
CREATE INDEX export_records_org_idx ON export_records (organization_id, created_at DESC);

CREATE TABLE retention_policies (
    organization_id   text PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    event_retention_days integer NOT NULL DEFAULT 30,
    usage_retention_days integer NOT NULL DEFAULT 90,
    updated_at        timestamptz NOT NULL DEFAULT now()
);

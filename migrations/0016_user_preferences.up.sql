-- rs:critical=true
-- M14 developer console: durable user view preferences and the dashboard
-- aggregate rows the console reads. Every metric is derived from the run,
-- issue and audit tables the console already owns; nothing here is a counter
-- the client invents.

CREATE TABLE user_preferences (
    user_id          text PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    view             jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

-- The dashboard is a projection over the run and issue tables, so no extra
-- storage is required for it; the index exists because the console lists the
-- most recent runs on every page load.
CREATE INDEX dashboard_recent_runs_idx ON runs (organization_id, created_at DESC);

-- rs:critical=true
-- RepoSurgeon M00 baseline schema.
--
-- The schema_migrations ledger shape mirrors internal/migrations.EnsureLedger
-- so that a fresh database bootstrapped by `make migrate-up` and one created
-- from this file are identical.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     bigint PRIMARY KEY,
    name        text NOT NULL,
    checksum    text NOT NULL,
    critical    boolean NOT NULL DEFAULT true,
    applied_at  timestamptz NOT NULL DEFAULT now()
);

-- Application metadata: deployment facts written by the services themselves.
-- Never store secrets here; values are logged and exported.
CREATE TABLE IF NOT EXISTS app_metadata (
    key         text PRIMARY KEY,
    value       text NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

COMMENT ON TABLE app_metadata IS
    'Non-secret deployment metadata; never store credential material';

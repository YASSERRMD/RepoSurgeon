-- rs:critical=true
-- Reverse of 0001_baseline. The schema_migrations ledger is owned by the
-- migration runner (internal/migrations), which deletes the ledger row for
-- this version in the same transaction, so only app_metadata is dropped here.
DROP TABLE IF EXISTS app_metadata;

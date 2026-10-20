-- rs:critical=true
-- M15 down: remove export_records and retention_policies tables.
DROP TABLE IF EXISTS export_records;
DROP TABLE IF EXISTS retention_policies;

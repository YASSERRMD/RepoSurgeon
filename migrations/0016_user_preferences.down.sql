-- rs:critical=true
-- Reverse of M14 developer console preferences. The run index is dropped
-- explicitly rather than relying on the table drop cascade.
DROP INDEX IF EXISTS dashboard_recent_runs_idx;
DROP TABLE IF EXISTS user_preferences;

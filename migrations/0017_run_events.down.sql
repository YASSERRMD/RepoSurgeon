-- rs:critical=true
-- M15 down: remove run events table and its trigger/function.
DROP TRIGGER IF EXISTS run_events_no_update ON run_events;
DROP FUNCTION IF EXISTS run_events_immutable();
DROP TABLE IF EXISTS run_events;

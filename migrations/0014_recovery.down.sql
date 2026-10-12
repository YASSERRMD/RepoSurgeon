-- rs:critical=true
-- Reverse of M12 recovery. Dropping in reverse dependency order keeps the
-- cascade safe should any other object depend on these tables later.
DROP TABLE IF EXISTS checkpoints;
DROP TABLE IF EXISTS effect_receipts;
DROP TABLE IF EXISTS recovery_budgets;
DROP TABLE IF EXISTS recovery_attempts;
DROP TABLE IF EXISTS recovery_events;

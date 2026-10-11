-- rs:critical=true
-- Reverse of 0010_github_bindings.
DROP TABLE IF EXISTS webhook_deliveries;
DROP TABLE IF EXISTS github_installations;

ALTER TABLE issues
    DROP COLUMN IF EXISTS sync_cursor,
    DROP COLUMN IF EXISTS imported_by,
    DROP COLUMN IF EXISTS labels,
    DROP COLUMN IF EXISTS assignees,
    DROP COLUMN IF EXISTS comments_count,
    DROP COLUMN IF EXISTS etag;

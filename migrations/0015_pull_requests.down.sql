-- rs:critical=true
-- Reverse of M13 issue-to-PR lifecycle, dropping in reverse dependency order.
DROP TABLE IF EXISTS merge_actions;
DROP TABLE IF EXISTS pr_commits;
DROP TABLE IF EXISTS pull_requests;

-- rs:critical=true
-- M04 GitHub bindings: installations connected to organizations, delivery
-- de-duplication for webhooks and issue sync cursors.

CREATE TABLE github_installations (
    id                text PRIMARY KEY,
    organization_id   text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    installation_id   bigint NOT NULL,
    account_login     text NOT NULL,
    app_slug          text NOT NULL DEFAULT '',
    permissions       jsonb NOT NULL DEFAULT '{}'::jsonb,
    status            text NOT NULL DEFAULT 'active'
                          CHECK (status IN ('active', 'suspended', 'removed')),
    created_by        text REFERENCES users (id) ON DELETE SET NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (organization_id, installation_id)
);

CREATE INDEX github_installations_org_idx ON github_installations (organization_id);

-- Webhook deliveries are recorded so a replayed delivery ID is ignored.
CREATE TABLE webhook_deliveries (
    id               text PRIMARY KEY,
    installation_id  bigint,
    event_type       text NOT NULL,
    action           text NOT NULL DEFAULT '',
    payload_hash     text NOT NULL,
    processed_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX webhook_deliveries_installation_idx ON webhook_deliveries (installation_id, processed_at DESC);

-- Issues gain explicit sync metadata: the external id/etag and cursor.
ALTER TABLE issues
    ADD COLUMN IF NOT EXISTS etag text,
    ADD COLUMN IF NOT EXISTS comments_count integer NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS assignees jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS labels jsonb NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS imported_by text REFERENCES users (id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS sync_cursor timestamptz;

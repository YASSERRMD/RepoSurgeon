-- rs:critical=true
-- M01 approvals, model usage accounting and credential metadata. Only metadata
-- about credentials is stored here — never the secret material itself.
CREATE TABLE approvals (
    id               text PRIMARY KEY,
    run_id           text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id       text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    action           text NOT NULL,
    requested_reason text NOT NULL DEFAULT '',
    state            text NOT NULL
                         CHECK (state IN ('pending', 'approved', 'rejected', 'expired', 'cancelled')),
    actor_id         text REFERENCES users (id) ON DELETE SET NULL,
    decision_reason  text NOT NULL DEFAULT '',
    expires_at       timestamptz,
    decided_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX approvals_org_state_idx ON approvals (organization_id, state, created_at DESC);
CREATE INDEX approvals_run_idx ON approvals (run_id);

CREATE TABLE model_usage (
    id               text PRIMARY KEY,
    run_id           text NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id       text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    provider         text NOT NULL,
    model            text NOT NULL,
    prompt_tokens    bigint NOT NULL DEFAULT 0,
    cached_tokens    bigint NOT NULL DEFAULT 0,
    output_tokens    bigint NOT NULL DEFAULT 0,
    usd_cost         numeric(14, 6) NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX model_usage_run_idx ON model_usage (run_id, created_at);

CREATE TABLE credential_metadata (
    id               text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id       text REFERENCES projects (id) ON DELETE CASCADE,
    kind             text NOT NULL CHECK (kind IN ('github_app', 'stepfun', 'other')),
    label            text NOT NULL,
    fingerprint      text NOT NULL,
    last_used_at     timestamptz,
    created_by       text REFERENCES users (id) ON DELETE SET NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    deleted_at       timestamptz,
    UNIQUE (organization_id, kind, fingerprint)
);

CREATE INDEX credential_metadata_org_idx ON credential_metadata (organization_id, created_at DESC);

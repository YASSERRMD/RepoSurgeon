-- rs:critical=true
-- M01 idempotency ledger for mutating requests. A repeated Idempotency-Key
-- returns the recorded response instead of performing the operation twice.
CREATE TABLE idempotency_keys (
    key              text PRIMARY KEY,
    organization_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    endpoint         text NOT NULL,
    request_hash     text NOT NULL,
    response_status  integer NOT NULL,
    response_body    jsonb NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL
);

CREATE INDEX idempotency_keys_expiry_idx ON idempotency_keys (expires_at);

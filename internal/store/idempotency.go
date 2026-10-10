package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Idempotency is the ledger that lets a mutating request be replayed safely.
// A repeated Idempotency-Key returns the recorded response instead of
// performing the operation twice, so writes are keyed rather than retried.
type Idempotency struct {
	pool *pgxpool.Pool
}

// NewIdempotency returns a repository bound to the connection pool.
func NewIdempotency(pool *pgxpool.Pool) *Idempotency {
	return &Idempotency{pool: pool}
}

// Get returns the recorded response for key. An expired or unknown key is
// reported as not found, which tells the caller to perform the operation.
func (r *Idempotency) Get(ctx context.Context, key string) (status int, body map[string]any, found bool, err error) {
	if !nonEmpty(key) {
		return 0, nil, false, validationErrorf("idempotency_key", "key is required")
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	err = r.pool.QueryRow(ctx,
		`SELECT response_status, response_body FROM idempotency_keys
		  WHERE key = $1 AND expires_at > now()`, key).Scan(&status, &body)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil, false, nil
		}
		return 0, nil, false, fmt.Errorf("get idempotency key %s: %w", key, err)
	}
	return status, body, true, nil
}

// Store records the response for key, expiring after ttl. A concurrent request
// that recorded the same key first wins: the insert does nothing and the
// recorded response is preserved, so the caller must Get to detect the race.
func (r *Idempotency) Store(ctx context.Context, key, organizationID, endpoint, requestHash string, status int, body map[string]any, ttl time.Duration) error {
	if !nonEmpty(key, organizationID, endpoint, requestHash) {
		return validationErrorf("idempotency_key", "key, organization_id, endpoint and request_hash are required")
	}
	if status <= 0 {
		return validationErrorf("status", "response status must be a positive HTTP status")
	}
	if ttl <= 0 {
		return validationErrorf("ttl", "idempotency ttl must be positive")
	}
	if body == nil {
		body = map[string]any{}
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO idempotency_keys (key, organization_id, endpoint, request_hash, response_status, response_body, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, now() + make_interval(secs => $7::double precision))
		 ON CONFLICT (key) DO NOTHING`,
		key, organizationID, endpoint, requestHash, status, body, ttl.Seconds()); err != nil {
		if isForeignKeyViolation(err) {
			return fmt.Errorf("organization %s: %w", organizationID, domain.ErrNotFound)
		}
		return fmt.Errorf("store idempotency key %s: %w", key, err)
	}
	return nil
}

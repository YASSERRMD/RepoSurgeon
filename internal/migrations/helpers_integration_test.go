package migrations

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func openTestPool(t *testing.T, dsn string) (*pgxpool.Pool, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func tableExists(ctx context.Context, t *testing.T, s Store, name string) bool {
	t.Helper()
	var exists bool
	if err := s.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&exists); err != nil {
		t.Fatalf("tableExists(%s): %v", name, err)
	}
	return exists
}

// resetVersions removes ledger rows for the given versions so tests that reuse
// a shared database never observe another test's drift.
func resetVersions(ctx context.Context, t *testing.T, s Store, versions ...int64) {
	t.Helper()
	if err := EnsureLedger(ctx, s); err != nil {
		t.Fatalf("EnsureLedger: %v", err)
	}
	for _, v := range versions {
		if _, err := s.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, v); err != nil {
			t.Fatalf("reset version %d: %v", v, err)
		}
	}
}

// prepare clears ledger rows and stale tables owned by a test so a database
// reused across runs always starts from the same state. Table names are
// hard-coded in tests, never derived from untrusted input.
func prepare(ctx context.Context, t *testing.T, s Store, tables []string, versions ...int64) {
	t.Helper()
	resetVersions(ctx, t, s, versions...)
	for _, table := range tables {
		if _, err := s.Exec(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}
}

// ledgerCountFor counts ledger rows for the given versions only.
func ledgerCountFor(ctx context.Context, t *testing.T, s Store, versions ...int64) int {
	t.Helper()
	if err := EnsureLedger(ctx, s); err != nil {
		t.Fatalf("EnsureLedger: %v", err)
	}
	var count int
	if err := s.QueryRow(ctx,
		`SELECT count(*) FROM schema_migrations WHERE version = ANY($1)`, versions).Scan(&count); err != nil {
		t.Fatalf("ledgerCountFor: %v", err)
	}
	return count
}

func TestMain(m *testing.M) {
	// Integration tests need a disposable PostgreSQL database; unit tests run
	// without one.
	os.Exit(m.Run())
}

package migrations

import (
	"context"
	"os"
	"testing"
)

// Integration tests run against a real PostgreSQL instance. They are skipped
// unless RS_TEST_DATABASE_URL points at a disposable database, and are wired
// into CI through the postgres service container. Each test uses its own
// version range so a shared database cannot couple tests together.
func testStore(t *testing.T) Store {
	t.Helper()
	dsn := os.Getenv("RS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RS_TEST_DATABASE_URL not set; skipping PostgreSQL integration test")
	}
	store, err := openTestPool(t, dsn)
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func TestUpAppliesAndDownReverses(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	prepare(ctx, t, s, []string{"it_first", "it_second"}, 1001, 1002)

	dir := t.TempDir()
	writeMigration(t, dir, "1001_it_first.up.sql", "CREATE TABLE it_first (id integer PRIMARY KEY);")
	writeMigration(t, dir, "1001_it_first.down.sql", "DROP TABLE it_first;")
	writeMigration(t, dir, "1002_it_second.up.sql", "CREATE TABLE it_second (id integer PRIMARY KEY);")
	writeMigration(t, dir, "1002_it_second.down.sql", "DROP TABLE it_second;")

	applied, err := Up(ctx, s, dir)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("applied = %d, want 2", len(applied))
	}
	if !tableExists(ctx, t, s, "it_first") || !tableExists(ctx, t, s, "it_second") {
		t.Fatal("tables missing after Up")
	}
	if ledgerCountFor(ctx, t, s, 1001, 1002) != 2 {
		t.Fatal("ledger should record two migrations")
	}

	// Re-running Up is a no-op.
	again, err := Up(ctx, s, dir)
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second Up applied %d migrations, want 0", len(again))
	}

	reversed, err := Down(ctx, s, dir, 1)
	if err != nil {
		t.Fatalf("Down: %v", err)
	}
	if len(reversed) != 1 || reversed[0].Version != 1002 {
		t.Fatalf("reversed = %+v", reversed)
	}
	if tableExists(ctx, t, s, "it_second") {
		t.Fatal("it_second should be dropped")
	}
	if !tableExists(ctx, t, s, "it_first") {
		t.Fatal("it_first must survive a single-step rollback")
	}

	// Reverse exactly what this test applied (the runner refuses to reverse a
	// version whose file is missing, which protects against foreign ledger
	// rows).
	if _, err := Down(ctx, s, dir, 2); err != nil {
		t.Fatalf("Down all: %v", err)
	}
	if tableExists(ctx, t, s, "it_first") {
		t.Fatal("it_first should be dropped")
	}
	if ledgerCountFor(ctx, t, s, 1001, 1002) != 0 {
		t.Fatal("ledger must be empty after full rollback")
	}
}

func TestPendingDetectsDrift(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	prepare(ctx, t, s, []string{"drift_t"}, 1003)

	dir := t.TempDir()
	writeMigration(t, dir, "1003_drift.up.sql", "CREATE TABLE drift_t (id integer);")
	writeMigration(t, dir, "1003_drift.down.sql", "DROP TABLE drift_t;")
	if _, err := Up(ctx, s, dir); err != nil {
		t.Fatalf("Up: %v", err)
	}
	t.Cleanup(func() {
		prepare(ctx, t, s, []string{"drift_t"}, 1003)
	})

	writeMigration(t, dir, "1003_drift.up.sql", "CREATE TABLE drift_t (id integer, extra text);")
	if _, err := Pending(ctx, s, dir); err == nil {
		t.Fatal("expected drift error after editing an applied migration")
	}
}

func TestUpRollsBackFailedMigration(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	prepare(ctx, t, s, []string{"ok_tx"}, 1004, 1005)

	dir := t.TempDir()
	writeMigration(t, dir, "1004_ok_tx.up.sql", "CREATE TABLE ok_tx (id integer);")
	writeMigration(t, dir, "1004_ok_tx.down.sql", "DROP TABLE ok_tx;")
	writeMigration(t, dir, "1005_bad_tx.up.sql", "CREATE TABLE ok_tx (id integer);")
	writeMigration(t, dir, "1005_bad_tx.down.sql", "DROP TABLE ok_tx;")

	_, err := Up(ctx, s, dir)
	if err == nil {
		t.Fatal("expected error for duplicate table")
	}
	t.Cleanup(func() {
		prepare(ctx, t, s, []string{"ok_tx"}, 1004, 1005)
	})

	if ledgerCountFor(ctx, t, s, 1004, 1005) != 1 {
		t.Fatalf("failed migration must not be recorded; ledger = %d", ledgerCountFor(ctx, t, s, 1004, 1005))
	}
	if !tableExists(ctx, t, s, "ok_tx") {
		t.Fatal("first migration must remain applied after the second fails")
	}
}

func TestAppliedLedgerRecordsChecksum(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	prepare(ctx, t, s, []string{"ledger_t"}, 1006)

	dir := t.TempDir()
	writeMigration(t, dir, "1006_ledger.up.sql", "CREATE TABLE ledger_t (id integer);")
	writeMigration(t, dir, "1006_ledger.down.sql", "DROP TABLE ledger_t;")
	if _, err := Up(ctx, s, dir); err != nil {
		t.Fatalf("Up: %v", err)
	}
	t.Cleanup(func() {
		prepare(ctx, t, s, []string{"ledger_t"}, 1006)
	})

	applied, err := Applied(ctx, s)
	if err != nil {
		t.Fatalf("Applied: %v", err)
	}
	rec, ok := applied[1006]
	if !ok {
		t.Fatal("version 1006 missing from ledger")
	}
	if rec.Name != "ledger" || rec.Checksum == "" || rec.AppliedAt.IsZero() {
		t.Fatalf("record = %+v", rec)
	}
}

func TestDownRefusesDriftedMigration(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	prepare(ctx, t, s, []string{"down_drift"}, 1007)

	dir := t.TempDir()
	writeMigration(t, dir, "1007_down_drift.up.sql", "CREATE TABLE down_drift (id integer);")
	writeMigration(t, dir, "1007_down_drift.down.sql", "DROP TABLE down_drift;")
	if _, err := Up(ctx, s, dir); err != nil {
		t.Fatalf("Up: %v", err)
	}
	t.Cleanup(func() {
		prepare(ctx, t, s, []string{"down_drift"}, 1007)
	})

	writeMigration(t, dir, "1007_down_drift.up.sql", "CREATE TABLE down_drift (id integer, extra text);")
	if _, err := Down(ctx, s, dir, 1); err == nil {
		t.Fatal("expected drift refusal when reversing a modified migration")
	}
	if !tableExists(ctx, t, s, "down_drift") {
		t.Fatal("table must survive a refused rollback")
	}
}

func TestDownRejectsZeroSteps(t *testing.T) {
	s := testStore(t)
	if _, err := Down(context.Background(), s, t.TempDir(), 0); err == nil {
		t.Fatal("expected error for zero steps")
	}
}

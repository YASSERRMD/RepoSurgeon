package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
)

type fakeDB struct {
	err error
}

func (f fakeDB) Ping(context.Context) error { return f.err }

func fixedNow() time.Time { return time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC) }

func findCheck(r Report, name string) (Check, bool) {
	for _, c := range r.Checks {
		if c.Name == name {
			return c, true
		}
	}
	return Check{}, false
}

func TestReportReadyWhenAllDependenciesHealthy(t *testing.T) {
	c := &Checker{
		DB: fakeDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) {
			return []migrations.Migration{{Version: 2, Name: "optional", Critical: false}}, nil
		},
		Now: fixedNow,
	}
	report := c.Check(context.Background())
	if report.Status != ReportReady {
		t.Fatalf("status = %q, want ready", report.Status)
	}
	db, ok := findCheck(report, "database")
	if !ok || db.Status != StatusUp {
		t.Fatalf("database check = %+v", db)
	}
	mig, ok := findCheck(report, "migrations")
	if !ok || mig.Status != StatusUp {
		t.Fatalf("migrations check = %+v", mig)
	}
	if !report.GeneratedAt.Equal(fixedNow()) {
		t.Fatalf("generated_at = %s", report.GeneratedAt)
	}
}

func TestReportUnavailableWhenDatabaseDown(t *testing.T) {
	c := &Checker{
		DB: fakeDB{err: errors.New("connection refused")},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) {
			return nil, nil
		},
		Now: fixedNow,
	}
	report := c.Check(context.Background())
	if report.Status != ReportUnavailable {
		t.Fatalf("status = %q, want unavailable", report.Status)
	}
	db, _ := findCheck(report, "database")
	if db.Status != StatusDown || db.Detail == "" {
		t.Fatalf("database check = %+v, want actionable detail", db)
	}
	// Migration check is skipped once the database is down.
	if _, ok := findCheck(report, "migrations"); ok {
		t.Fatal("migration check should not run when the database is down")
	}
}

func TestReportUnavailableWhenCriticalMigrationPending(t *testing.T) {
	c := &Checker{
		DB: fakeDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) {
			return []migrations.Migration{{Version: 3, Name: "add_runs", Critical: true}}, nil
		},
		Now: fixedNow,
	}
	report := c.Check(context.Background())
	if report.Status != ReportUnavailable {
		t.Fatalf("status = %q, want unavailable", report.Status)
	}
	mig, _ := findCheck(report, "migrations")
	if mig.Status != StatusDown {
		t.Fatalf("migrations check = %+v", mig)
	}
	if want := "0003_add_runs.up.sql"; !contains(mig.Detail, want) {
		t.Fatalf("detail = %q, want mention of %s", mig.Detail, want)
	}
}

func TestReportUnavailableWhenMigrationStateUnknown(t *testing.T) {
	c := &Checker{
		DB: fakeDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) {
			return nil, errors.New("permission denied")
		},
		Now: fixedNow,
	}
	report := c.Check(context.Background())
	if report.Status != ReportUnavailable {
		t.Fatalf("status = %q, want unavailable", report.Status)
	}
	mig, _ := findCheck(report, "migrations")
	if mig.Status != StatusDown {
		t.Fatalf("migrations check = %+v", mig)
	}
}

func TestReportHandlesMissingDependencies(t *testing.T) {
	c := &Checker{Now: fixedNow}
	report := c.Check(context.Background())
	if report.Status != ReportUnavailable {
		t.Fatalf("status = %q, want unavailable", report.Status)
	}
	// The database check fails first, so the migration check is skipped.
	if len(report.Checks) != 1 {
		t.Fatalf("checks = %+v", report.Checks)
	}
	if report.Checks[0].Name != "database" || report.Checks[0].Detail == "" {
		t.Fatalf("database check = %+v", report.Checks[0])
	}
}

func TestReportUnavailableWhenMigrationSourceMissing(t *testing.T) {
	c := &Checker{DB: fakeDB{}, Now: fixedNow}
	report := c.Check(context.Background())
	if report.Status != ReportUnavailable {
		t.Fatalf("status = %q, want unavailable", report.Status)
	}
	if len(report.Checks) != 2 {
		t.Fatalf("checks = %+v", report.Checks)
	}
	mig, _ := findCheck(report, "migrations")
	if mig.Status != StatusDown {
		t.Fatalf("migrations check = %+v", mig)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

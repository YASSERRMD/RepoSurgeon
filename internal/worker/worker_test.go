package worker

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/health"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func TestHealthHandlerReady(t *testing.T) {
	sup := &Supervisor{
		DB:                fakeDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
	}
	rec := httptest.NewRecorder()
	HealthHandler(sup)(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestHealthHandlerUnavailable(t *testing.T) {
	sup := &Supervisor{
		DB:                fakeDB{err: errors.New("db down")},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
	}
	rec := httptest.NewRecorder()
	HealthHandler(sup)(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestRunStopsAfterThreshold(t *testing.T) {
	sup := &Supervisor{
		DB:                  fakeDB{err: errors.New("db down")},
		PendingMigrations:   func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Interval:            time.Millisecond,
		UnhealthyBeforeExit: 2,
	}
	err := sup.Run(context.Background())
	if err == nil || !errors.Is(err, errUnhealthy) {
		t.Fatalf("err = %v, want errUnhealthy", err)
	}
}

func TestRunReturnsWhenContextCancelled(t *testing.T) {
	sup := &Supervisor{
		DB:                fakeDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Interval:          10 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := sup.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunRecoversAfterTransientFailure(t *testing.T) {
	calls := 0
	sup := &Supervisor{
		DB: fakeDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) {
			calls++
			if calls <= 2 {
				return []migrations.Migration{{Version: 9, Name: "late", Critical: true}}, nil
			}
			return nil, nil
		},
		Interval:            5 * time.Millisecond,
		UnhealthyBeforeExit: 5,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := sup.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if calls < 3 {
		t.Fatalf("expected recovery, calls = %d", calls)
	}
}

func TestCycleReport(t *testing.T) {
	sup := &Supervisor{
		DB:                fakeDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
	}
	report := sup.Cycle(context.Background())
	if report.Status != health.ReportReady {
		t.Fatalf("status = %q", report.Status)
	}
}

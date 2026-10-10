// Package worker hosts the background process that will own the durable agent
// queue (added in M06). In M00 it performs the real duties a long-running
// worker has today: verify the database and migration state, serve a health
// endpoint, and escalate to a non-zero exit when the database stays
// unreachable so the orchestrator restarts it.
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/health"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
)

// Supervisor is the worker runtime loop.
type Supervisor struct {
	DB                health.Database
	PendingMigrations func(ctx context.Context) ([]migrations.Migration, error)
	Logger            *slog.Logger
	Interval          time.Duration
	// UnhealthyBeforeExit is the number of consecutive failed cycles that
	// terminates the process so the container runtime restarts it.
	UnhealthyBeforeExit int
	Now                 func() time.Time
}

// Defaults.
const (
	DefaultInterval            = 30 * time.Second
	DefaultUnhealthyBeforeExit = 3
)

// Cycle is the outcome of one supervision iteration.
type Cycle struct {
	Report              health.Report
	ConsecutiveFailures int
}

// Run executes supervision cycles until ctx is canceled or the unhealthy
// threshold is reached.
func (s *Supervisor) Run(ctx context.Context) error {
	interval := s.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	threshold := s.UnhealthyBeforeExit
	if threshold <= 0 {
		threshold = DefaultUnhealthyBeforeExit
	}
	log := s.Logger
	if log == nil {
		log = slog.Default()
	}

	failures := 0
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		report := s.Cycle(ctx)
		if report.Status == health.ReportReady {
			if failures > 0 {
				log.Info("worker healthy again", "previous_failures", failures)
			}
			failures = 0
			continue
		}
		failures++
		for _, c := range report.Checks {
			if c.Status == health.StatusDown {
				log.Warn("worker dependency unhealthy", "check", c.Name, "detail", c.Detail, "consecutive_failures", failures)
			}
		}
		if failures >= threshold {
			return fmt.Errorf("worker dependencies unhealthy for %d consecutive cycles: %w", failures, errUnhealthy)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Cycle performs one immediate supervision evaluation.
func (s *Supervisor) Cycle(ctx context.Context) health.Report {
	checker := &health.Checker{
		DB:                s.DB,
		PendingMigrations: s.PendingMigrations,
		Now:               s.Now,
	}
	return checker.Check(ctx)
}

var errUnhealthy = errors.New("dependencies unhealthy")

// HealthHandler serves the worker readiness report as JSON.
func HealthHandler(sup *Supervisor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		report := sup.Cycle(r.Context())
		status := http.StatusOK
		if report.Status != health.ReportReady {
			status = http.StatusServiceUnavailable
		}
		writeJSON(w, status, report)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

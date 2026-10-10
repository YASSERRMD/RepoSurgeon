// Package health computes process readiness. Readiness is the honest answer to
// "can this process serve traffic right now": the database must be reachable
// and no critical migration may be pending.
package health

import (
	"context"
	"fmt"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
)

// Status values reported by checks.
const (
	StatusUp   = "up"
	StatusDown = "down"
)

// Report is the readiness verdict for a process.
const (
	ReportReady       = "ready"
	ReportUnavailable = "unavailable"
)

// Database is the narrow connectivity surface health needs.
type Database interface {
	Ping(ctx context.Context) error
}

// Check is one evaluated dependency.
type Check struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// Report is the full readiness report.
type Report struct {
	Status      string    `json:"status"`
	Checks      []Check   `json:"checks"`
	GeneratedAt time.Time `json:"generated_at"`
}

// Checker evaluates readiness for one process.
type Checker struct {
	DB                Database
	PendingMigrations func(ctx context.Context) ([]migrations.Migration, error)
	Timeout           time.Duration
	Now               func() time.Time
}

// DefaultTimeout bounds a single readiness evaluation.
const DefaultTimeout = 5 * time.Second

// Check evaluates every dependency and returns the report. The report never
// contains credential material; library errors are reduced to a sanitized
// reason string.
func (c *Checker) Check(ctx context.Context) Report {
	if c.Timeout <= 0 {
		c.Timeout = DefaultTimeout
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()

	report := Report{Status: ReportReady, GeneratedAt: now().UTC(), Checks: []Check{}}

	dbCheck := c.checkDatabase(ctx)
	report.Checks = append(report.Checks, dbCheck)
	if dbCheck.Status != StatusUp {
		report.Status = ReportUnavailable
		return report
	}

	migCheck := c.checkMigrations(ctx)
	report.Checks = append(report.Checks, migCheck)
	if migCheck.Status != StatusUp {
		report.Status = ReportUnavailable
	}
	return report
}

func (c *Checker) checkDatabase(ctx context.Context) Check {
	start := time.Now()
	check := Check{Name: "database"}
	if c.DB == nil {
		check.Status = StatusDown
		check.Detail = "database handle is not configured"
		check.DurationMS = elapsedMS(start)
		return check
	}
	if err := c.DB.Ping(ctx); err != nil {
		check.Status = StatusDown
		check.Detail = "database unreachable; verify PostgreSQL is running and RS_DATABASE_URL is correct"
		check.DurationMS = elapsedMS(start)
		return check
	}
	check.Status = StatusUp
	check.DurationMS = elapsedMS(start)
	return check
}

func (c *Checker) checkMigrations(ctx context.Context) Check {
	start := time.Now()
	check := Check{Name: "migrations"}
	if c.PendingMigrations == nil {
		check.Status = StatusDown
		check.Detail = "migration source is not configured"
		check.DurationMS = elapsedMS(start)
		return check
	}
	pending, err := c.PendingMigrations(ctx)
	if err != nil {
		check.Status = StatusDown
		check.Detail = fmt.Sprintf("migration state unknown: %v", err)
		check.DurationMS = elapsedMS(start)
		return check
	}
	var critical []string
	for _, mg := range pending {
		if mg.Critical {
			critical = append(critical, mg.Filename())
		}
	}
	switch {
	case len(critical) > 0:
		check.Status = StatusDown
		check.Detail = fmt.Sprintf("%d critical migration(s) pending (%s); run `make migrate-up` before serving traffic", len(critical), summarize(critical))
	default:
		check.Status = StatusUp
		if len(pending) > 0 {
			check.Detail = fmt.Sprintf("%d optional migration(s) pending", len(pending))
		}
	}
	check.DurationMS = elapsedMS(start)
	return check
}

func summarize(names []string) string {
	const max = 3
	if len(names) <= max {
		return joinStrings(names)
	}
	return joinStrings(names[:max]) + fmt.Sprintf(", +%d more", len(names)-max)
}

func joinStrings(values []string) string {
	out := ""
	for i, v := range values {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

func elapsedMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

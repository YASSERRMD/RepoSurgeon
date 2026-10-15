package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UserPreferences is the durable view settings of one operator. The console
// reads it to restore the layout, filters and table density a person chose, so
// a reload of the page does not discard their working state.
type UserPreferences struct {
	// UserID is the principal the settings belong to.
	UserID string
	// OrganizationID is the tenant scope the settings were written in.
	OrganizationID string
	// View is the settings document the console round-trips unchanged.
	View map[string]any
	// UpdatedAt is when the settings were last written.
	UpdatedAt time.Time
}

// Preferences persists operator view settings.
type Preferences struct {
	pool *pgxpool.Pool
}

// NewPreferences binds the repository to the pool.
func NewPreferences(pool *pgxpool.Pool) *Preferences { return &Preferences{pool: pool} }

// Get returns the stored settings, or the empty document when none exist. A
// missing document is not an error: the console renders its defaults.
func (r *Preferences) Get(ctx context.Context, userID string) (UserPreferences, bool, error) {
	if userID == "" {
		return UserPreferences{}, false, domain.ErrValidation
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	var (
		prefs     UserPreferences
		view      []byte
		updatedAt time.Time
	)
	err := r.pool.QueryRow(ctx, `
        SELECT user_id, organization_id, view, updated_at
        FROM user_preferences WHERE user_id = $1`, userID).
		Scan(&prefs.UserID, &prefs.OrganizationID, &view, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return UserPreferences{}, false, nil
	}
	if err != nil {
		return UserPreferences{}, false, fmt.Errorf("preferences: get: %w", err)
	}
	if len(view) > 0 {
		if err := json.Unmarshal(view, &prefs.View); err != nil {
			return UserPreferences{}, false, fmt.Errorf("preferences: decode view: %w", err)
		}
	}
	if prefs.View == nil {
		prefs.View = map[string]any{}
	}
	prefs.UpdatedAt = updatedAt
	return prefs, true, nil
}

// Save stores the settings document for a user in an organization.
func (r *Preferences) Save(ctx context.Context, userID, organizationID string, view map[string]any) (UserPreferences, error) {
	if userID == "" || organizationID == "" {
		return UserPreferences{}, domain.ErrValidation
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		return UserPreferences{}, fmt.Errorf("preferences: encode view: %w", err)
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	var updatedAt time.Time
	err = r.pool.QueryRow(ctx, `
        INSERT INTO user_preferences (user_id, organization_id, view)
        VALUES ($1, $2, $3)
        ON CONFLICT (user_id) DO UPDATE
            SET organization_id = EXCLUDED.organization_id,
                view = EXCLUDED.view,
                updated_at = now()
        RETURNING updated_at`,
		userID, organizationID, encoded).Scan(&updatedAt)
	if err != nil {
		return UserPreferences{}, fmt.Errorf("preferences: save: %w", err)
	}
	return UserPreferences{UserID: userID, OrganizationID: organizationID, View: view, UpdatedAt: updatedAt}, nil
}

// DashboardRun is one recent run row the console lists.
type DashboardRun struct {
	// ID is the run identifier.
	ID string
	// ProjectID is the project the run belongs to.
	ProjectID string
	// ProjectName is the project display name, joined from the project table.
	ProjectName string
	// State is the run's recorded state.
	State string
	// Attempt is the attempt counter the state machine recorded.
	Attempt int
	// CreatedAt is when the run was created.
	CreatedAt time.Time
}

// DashboardRow is the aggregate the console reads on every page load. Every
// value is a count over the real tables, never a number the client invents.
type DashboardRow struct {
	// RunsByState counts the runs of the organization per state.
	RunsByState map[string]int `json:"runs_by_state"`
	// OpenIssues counts the issues that are not closed.
	OpenIssues int `json:"open_issues"`
	// BlockedRuns counts the runs parked as blocked.
	BlockedRuns int `json:"blocked_runs"`
	// FailedRuns counts the runs in the failed state.
	FailedRuns int `json:"failed_runs"`
	// RecentRuns are the newest runs, newest first.
	RecentRuns []DashboardRun `json:"recent_runs"`
	// PendingAuditEvents is the count of audit rows in the last 24 hours.
	RecentAuditEvents int `json:"recent_audit_events"`
	// GeneratedAt is when the aggregate was computed.
	GeneratedAt time.Time `json:"generated_at"`
}

// Dashboard aggregates the console's headline metrics from real tables.
type Dashboard struct {
	pool *pgxpool.Pool
}

// NewDashboard binds the repository to the pool.
func NewDashboard(pool *pgxpool.Pool) *Dashboard { return &Dashboard{pool: pool} }

// Aggregate computes the dashboard for one organization. The statements are
// deliberately separate so a failure reports which metric could not be read
// instead of a single opaque error.
func (r *Dashboard) Aggregate(ctx context.Context, organizationID string) (DashboardRow, error) {
	if organizationID == "" {
		return DashboardRow{}, domain.ErrValidation
	}
	row := DashboardRow{RunsByState: map[string]int{}, GeneratedAt: time.Now().UTC()}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	rows, err := r.pool.Query(ctx, `
        SELECT state, count(*) FROM runs WHERE organization_id = $1 GROUP BY state`, organizationID)
	if err != nil {
		return DashboardRow{}, fmt.Errorf("dashboard: runs by state: %w", err)
	}
	for rows.Next() {
		var (
			state string
			count int
		)
		if err := rows.Scan(&state, &count); err != nil {
			rows.Close()
			return DashboardRow{}, fmt.Errorf("dashboard: scan run state: %w", err)
		}
		row.RunsByState[state] = count
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return DashboardRow{}, fmt.Errorf("dashboard: run states: %w", err)
	}
	rows.Close()

	if err := r.pool.QueryRow(ctx, `
        SELECT count(*) FROM issues
        WHERE organization_id = $1 AND state NOT IN ('closed', 'canceled')`, organizationID).
		Scan(&row.OpenIssues); err != nil {
		return DashboardRow{}, fmt.Errorf("dashboard: open issues: %w", err)
	}
	if err := r.pool.QueryRow(ctx, `
        SELECT count(*) FROM runs WHERE organization_id = $1 AND state = 'blocked'`, organizationID).
		Scan(&row.BlockedRuns); err != nil {
		return DashboardRow{}, fmt.Errorf("dashboard: blocked runs: %w", err)
	}
	if err := r.pool.QueryRow(ctx, `
        SELECT count(*) FROM runs WHERE organization_id = $1 AND state = 'failed'`, organizationID).
		Scan(&row.FailedRuns); err != nil {
		return DashboardRow{}, fmt.Errorf("dashboard: failed runs: %w", err)
	}
	if err := r.pool.QueryRow(ctx, `
        SELECT count(*) FROM audit_events
        WHERE organization_id = $1 AND created_at >= now() - interval '24 hours'`, organizationID).
		Scan(&row.RecentAuditEvents); err != nil {
		return DashboardRow{}, fmt.Errorf("dashboard: recent audit events: %w", err)
	}

	runRows, err := r.pool.Query(ctx, `
        SELECT runs.id, runs.project_id, projects.name, runs.state, runs.attempt, runs.created_at
        FROM runs JOIN projects ON projects.id = runs.project_id
        WHERE runs.organization_id = $1
        ORDER BY runs.created_at DESC LIMIT 10`, organizationID)
	if err != nil {
		return DashboardRow{}, fmt.Errorf("dashboard: recent runs: %w", err)
	}
	defer runRows.Close()
	for runRows.Next() {
		var run DashboardRun
		if err := runRows.Scan(&run.ID, &run.ProjectID, &run.ProjectName, &run.State, &run.Attempt, &run.CreatedAt); err != nil {
			return DashboardRow{}, fmt.Errorf("dashboard: scan recent run: %w", err)
		}
		row.RecentRuns = append(row.RecentRuns, run)
	}
	if err := runRows.Err(); err != nil {
		return DashboardRow{}, fmt.Errorf("dashboard: recent runs rows: %w", err)
	}
	return row, nil
}

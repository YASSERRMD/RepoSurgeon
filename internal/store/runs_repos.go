package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/agent"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Run is one durable agent run.
type Run struct {
	ID             string
	OrganizationID string
	ProjectID      string
	TaskID         string
	IssueID        string
	State          string
	BaseSHA        string
	ModelConfig    map[string]any
	Limits         map[string]any
	Policy         map[string]any
	Attempt        int
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	StartedAt      *time.Time
	FinishedAt     *time.Time
}

// Runs owns the durable run state machine.
type Runs struct {
	pool *pgxpool.Pool
}

// NewRuns binds the repository to the pool.
func NewRuns(pool *pgxpool.Pool) *Runs { return &Runs{pool: pool} }

// Create inserts a run in the queued state.
func (r *Runs) Create(ctx context.Context, run Run) (Run, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	if run.State == "" {
		run.State = agent.StateQueued
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO runs (id, organization_id, project_id, task_id, issue_id, state, base_sha,
		                  model_config, limits, policy, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		RETURNING state, attempt, created_at, updated_at`,
		run.ID, run.OrganizationID, run.ProjectID, nilString(run.TaskID), nilString(run.IssueID),
		run.State, run.BaseSHA, orEmptyMap(run.ModelConfig), orEmptyMap(run.Limits),
		orEmptyMap(run.Policy), run.CreatedBy,
	).Scan(&run.State, &run.Attempt, &run.CreatedAt, &run.UpdatedAt)
	if err != nil {
		return Run{}, fmt.Errorf("runs: create: %w", err)
	}
	return run, nil
}

// Get returns a run scoped to an organization.
func (r *Runs) Get(ctx context.Context, organizationID, runID string) (Run, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	var run Run
	var taskID, issueID *string
	var startedAt, finishedAt *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, project_id, task_id, issue_id, state, coalesce(base_sha, ''),
		       model_config, limits, policy, attempt, coalesce(created_by, ''),
		       created_at, updated_at, started_at, finished_at
		FROM runs
		WHERE organization_id = $1 AND id = $2`, organizationID, runID).
		Scan(&run.ID, &run.OrganizationID, &run.ProjectID, &taskID, &issueID, &run.State, &run.BaseSHA,
			&run.ModelConfig, &run.Limits, &run.Policy, &run.Attempt, &run.CreatedBy,
			&run.CreatedAt, &run.UpdatedAt, &startedAt, &finishedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, fmt.Errorf("run %s: %w", runID, agent.ErrRunNotFound)
		}
		return Run{}, fmt.Errorf("runs: get: %w", err)
	}
	if taskID != nil {
		run.TaskID = *taskID
	}
	if issueID != nil {
		run.IssueID = *issueID
	}
	run.StartedAt = startedAt
	run.FinishedAt = finishedAt
	return run, nil
}

// Transition moves a run to a new state, refusing illegal transitions and
// recording the move in run_state_history. The current state is read inside the
// same transaction so two workers cannot race.
func (r *Runs) Transition(ctx context.Context, organizationID, runID, toState, reason, actorType string) (Run, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	if !agent.Known(toState) {
		return Run{}, fmt.Errorf("runs: unknown state %q: %w", toState, agent.ErrRunNotFound)
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("runs: begin transition: %w", err)
	}
	defer rollbackTx(ctx, tx)

	var from string
	if err := tx.QueryRow(ctx, `SELECT state FROM runs WHERE organization_id = $1 AND id = $2 FOR UPDATE`,
		organizationID, runID).Scan(&from); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Run{}, fmt.Errorf("run %s: %w", runID, agent.ErrRunNotFound)
		}
		return Run{}, fmt.Errorf("runs: read state: %w", err)
	}
	if err := agent.Transition(from, toState); err != nil {
		return Run{}, err
	}

	var run Run
	var taskID, issueID, startedAt, finishedAt *string
	if err := tx.QueryRow(ctx, `
		UPDATE runs
		SET state = $3,
		    attempt = attempt + 1,
		    started_at = coalesce(started_at, CASE WHEN $3 <> 'queued' THEN now() END),
		    finished_at = CASE WHEN $3 IN ('completed', 'failed', 'cancelled') THEN now() END,
		    updated_at = now()
		WHERE organization_id = $1 AND id = $2
		RETURNING id, organization_id, project_id, task_id, issue_id, state, coalesce(base_sha, ''),
		          model_config, limits, policy, attempt, coalesce(created_by, ''),
		          created_at, updated_at, started_at, finished_at`,
		organizationID, runID, toState).
		Scan(&run.ID, &run.OrganizationID, &run.ProjectID, &taskID, &issueID, &run.State, &run.BaseSHA,
			&run.ModelConfig, &run.Limits, &run.Policy, &run.Attempt, &run.CreatedBy,
			&run.CreatedAt, &run.UpdatedAt, &startedAt, &finishedAt); err != nil {
		return Run{}, fmt.Errorf("runs: update state: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO run_state_history (id, run_id, from_state, to_state, reason, actor_type)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		newHistoryID(), runID, from, toState, reason, actorType); err != nil {
		return Run{}, fmt.Errorf("runs: record history: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Run{}, fmt.Errorf("runs: commit transition: %w", err)
	}
	return run, nil
}

// ListForProject returns a project's runs, newest first.
func (r *Runs) ListForProject(ctx context.Context, projectID string, limit int, cursor string) (Page[Run], error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, project_id, task_id, issue_id, state, coalesce(base_sha, ''),
		       model_config, limits, policy, attempt, coalesce(created_by, ''),
		       created_at, updated_at, started_at, finished_at
		FROM runs
		WHERE project_id = $1
		ORDER BY created_at DESC, id DESC LIMIT $2`,
		projectID, int64(clampLimit(limit)+1))
	if err != nil {
		return Page[Run]{}, fmt.Errorf("runs: list: %w", err)
	}
	defer rows.Close()
	var items []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return Page[Run]{}, err
		}
		items = append(items, run)
	}
	if err := rows.Err(); err != nil {
		return Page[Run]{}, fmt.Errorf("runs: iterate: %w", err)
	}
	return pageFromRows(items, clampLimit(limit), func(run Run) string {
		return createdCursor(run.CreatedAt, run.ID)
	}), nil
}

// History returns the recorded state transitions for a run.
func (r *Runs) History(ctx context.Context, runID string) ([]StateChange, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	rows, err := r.pool.Query(ctx, `
		SELECT id, run_id, coalesce(from_state, ''), to_state, reason, actor_type, created_at
		FROM run_state_history
		WHERE run_id = $1 ORDER BY created_at`, runID)
	if err != nil {
		return nil, fmt.Errorf("runs: history: %w", err)
	}
	defer rows.Close()
	var out []StateChange
	for rows.Next() {
		var change StateChange
		if err := rows.Scan(&change.ID, &change.RunID, &change.FromState, &change.ToState,
			&change.Reason, &change.ActorType, &change.CreatedAt); err != nil {
			return nil, fmt.Errorf("runs: scan history: %w", err)
		}
		out = append(out, change)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("runs: iterate history: %w", err)
	}
	return out, nil
}

// StateChange is one recorded transition.
type StateChange struct {
	ID        string
	RunID     string
	FromState string
	ToState   string
	Reason    string
	ActorType string
	CreatedAt time.Time
}

func scanRun(row scannable) (Run, error) {
	var run Run
	var taskID, issueID *string
	var startedAt, finishedAt *time.Time
	err := row.Scan(&run.ID, &run.OrganizationID, &run.ProjectID, &taskID, &issueID, &run.State, &run.BaseSHA,
		&run.ModelConfig, &run.Limits, &run.Policy, &run.Attempt, &run.CreatedBy,
		&run.CreatedAt, &run.UpdatedAt, &startedAt, &finishedAt)
	if err != nil {
		return Run{}, err
	}
	if taskID != nil {
		run.TaskID = *taskID
	}
	if issueID != nil {
		run.IssueID = *issueID
	}
	run.StartedAt = startedAt
	run.FinishedAt = finishedAt
	return run, nil
}

// newHistoryID returns a fresh history row id.
func newHistoryID() string { return ids.MustNew("hst") }

func nilString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func orEmptyMap(value map[string]any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	return value
}

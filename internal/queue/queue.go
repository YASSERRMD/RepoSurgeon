// Package queue implements the durable PostgreSQL task queue described in
// docs/adr/ADR-001: claim with FOR UPDATE SKIP LOCKED, lease, heartbeat and
// recover expired leases. A worker that dies mid-task never blocks progress;
// its lease expires and another worker claims the task with an incremented
// attempt counter.
package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
)

// Task kinds.
const (
	KindRun   = "run"
	KindSync  = "sync"
	KindProbe = "probe"
)

// Task statuses.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
	StatusCanceled  = "canceled"
)

// NoTaskError signals an empty claim result without wrapping the sentinel in
// every call site.
type NoTaskError struct{}

func (e *NoTaskError) Error() string { return domain.ErrNoTaskAvailable.Error() }

// Task is one durable unit of work.
type Task struct {
	ID             string
	OrganizationID string
	ProjectID      string
	RunID          string
	Kind           string
	Status         string
	Payload        map[string]any
	Attempt        int
	MaxAttempts    int
	AvailableAt    time.Time
	LeaseOwner     string
	LeaseExpiresAt *time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Queue persists tasks.
type Queue struct {
	pool pool
}

// pool is the narrow database surface this package needs.
type pool = queryExecutor

// New binds the queue to the database.
func New(executor queryExecutor) *Queue { return &Queue{pool: executor} }

// Enqueue inserts a pending task.
func (q *Queue) Enqueue(ctx context.Context, task Task) (Task, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	if task.ID == "" {
		return Task{}, errors.New("queue: task id is required")
	}
	if task.Kind == "" {
		return Task{}, errors.New("queue: task kind is required")
	}
	if task.MaxAttempts <= 0 {
		task.MaxAttempts = 5
	}
	var runRef any
	if task.RunID != "" {
		runRef = task.RunID
	}
	if task.Payload == nil {
		task.Payload = map[string]any{}
	}
	err := q.pool.QueryRow(ctx, `
		INSERT INTO task_queue (id, organization_id, project_id, run_id, kind, status, payload, max_attempts)
		VALUES ($1, $2, $3, $4, $5, 'pending', $6, $7)
		RETURNING status, attempt, available_at, created_at, updated_at`,
		task.ID, task.OrganizationID, task.ProjectID, runRef, task.Kind, task.Payload, task.MaxAttempts,
	).Scan(&task.Status, &task.Attempt, &task.AvailableAt, &task.CreatedAt, &task.UpdatedAt)
	if err != nil {
		return Task{}, fmt.Errorf("queue: enqueue: %w", err)
	}
	return task, nil
}

// Claim atomically leases one pending task for owner. Tasks whose lease
// expired are eligible again; a task that exhausted its attempts is not.
func (q *Queue) Claim(ctx context.Context, owner string, lease time.Duration) (Task, error) {
	return q.ClaimKind(ctx, owner, "", lease)
}

// ClaimKind leases one task, optionally restricted to a kind so a worker only
// takes work it can perform. Concurrent workers never receive the same row
// because the claim uses FOR UPDATE SKIP LOCKED.
func (q *Queue) ClaimKind(ctx context.Context, owner, kind string, lease time.Duration) (Task, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	leaseSeconds := lease.Seconds()
	var (
		task  Task
		runID *string
		err   error
	)
	if kind == "" {
		err = q.pool.QueryRow(ctx, `
		UPDATE task_queue
		SET status = 'running',
		    attempt = attempt + 1,
		    lease_owner = $1,
		    lease_expires_at = now() + make_interval(secs => $2),
		    last_heartbeat = now(),
		    updated_at = now()
		WHERE id = (
		    SELECT id FROM task_queue
		    WHERE (status = 'pending' AND available_at <= now())
		       OR (status = 'running' AND lease_expires_at < now())
		    ORDER BY available_at
		    FOR UPDATE SKIP LOCKED
		    LIMIT 1
		)
		RETURNING id, organization_id, project_id, run_id, kind, status, payload,
		          attempt, max_attempts, lease_owner, available_at, created_at, updated_at`,
			owner, leaseSeconds,
		).Scan(&task.ID, &task.OrganizationID, &task.ProjectID, &runID, &task.Kind,
			&task.Status, &task.Payload, &task.Attempt, &task.MaxAttempts, &task.LeaseOwner,
			&task.AvailableAt, &task.CreatedAt, &task.UpdatedAt)
	} else {
		err = q.pool.QueryRow(ctx, `
		UPDATE task_queue
		SET status = 'running',
		    attempt = attempt + 1,
		    lease_owner = $1,
		    lease_expires_at = now() + make_interval(secs => $2),
		    last_heartbeat = now(),
		    updated_at = now()
		WHERE id = (
		    SELECT id FROM task_queue
		    WHERE kind = $3
		      AND ((status = 'pending' AND available_at <= now())
		           OR (status = 'running' AND lease_expires_at < now()))
		    ORDER BY available_at
		    FOR UPDATE SKIP LOCKED
		    LIMIT 1
		)
		RETURNING id, organization_id, project_id, run_id, kind, status, payload,
		          attempt, max_attempts, lease_owner, available_at, created_at, updated_at`,
			owner, leaseSeconds, kind,
		).Scan(&task.ID, &task.OrganizationID, &task.ProjectID, &runID, &task.Kind,
			&task.Status, &task.Payload, &task.Attempt, &task.MaxAttempts, &task.LeaseOwner,
			&task.AvailableAt, &task.CreatedAt, &task.UpdatedAt)
	}
	if runID != nil {
		task.RunID = *runID
	}
	if err != nil {
		if isNoRows(err) {
			return Task{}, domain.ErrNoTaskAvailable
		}
		return Task{}, fmt.Errorf("queue: claim: %w", err)
	}
	return task, nil
}

// Heartbeat extends the lease of a running task owned by owner.
func (q *Queue) Heartbeat(ctx context.Context, owner, taskID string, lease time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	tag, err := q.pool.Exec(ctx, `
		UPDATE task_queue
		SET last_heartbeat = now(),
		    lease_expires_at = now() + make_interval(secs => $3),
		    updated_at = now()
		WHERE id = $1 AND lease_owner = $2 AND status = 'running'`,
		taskID, owner, lease.Seconds())
	if err != nil {
		return fmt.Errorf("queue: heartbeat: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("queue: lease for task %s is no longer held by %s", taskID, owner)
	}
	return nil
}

// Complete marks a task completed and releases the lease.
func (q *Queue) Complete(ctx context.Context, owner, taskID string) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	tag, err := q.pool.Exec(ctx, `
		UPDATE task_queue
		SET status = 'completed', lease_owner = NULL, lease_expires_at = NULL,
		    last_error = '', updated_at = now()
		WHERE id = $1 AND status = 'running'`, taskID)
	if err != nil {
		return fmt.Errorf("queue: complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("queue: task %s is not running: %w", taskID, domain.ErrNotFound)
	}
	return nil
}

// Fail records a failure. A task with attempts left is scheduled for retry;
// otherwise it is marked failed permanently.
func (q *Queue) Fail(ctx context.Context, owner, taskID, reason string, retryDelay time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	tag, err := q.pool.Exec(ctx, `
		UPDATE task_queue
		SET status = CASE WHEN attempt >= max_attempts THEN 'failed' ELSE 'pending' END,
		    last_error = $1,
		    lease_owner = NULL,
		    lease_expires_at = NULL,
		    available_at = now() + make_interval(secs => $2),
		    updated_at = now()
		WHERE id = $3 AND status = 'running'`, reason, retryDelay.Seconds(), taskID)
	if err != nil {
		return fmt.Errorf("queue: fail: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("queue: task %s is not running: %w", taskID, domain.ErrNotFound)
	}
	return nil
}

// Cancel marks a pending or running task canceled.
func (q *Queue) Cancel(ctx context.Context, taskID string) error {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	_, err := q.pool.Exec(ctx, `
		UPDATE task_queue
		SET status = 'canceled', lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		WHERE id = $1 AND status IN ('pending', 'running')`, taskID)
	if err != nil {
		return fmt.Errorf("queue: cancel: %w", err)
	}
	return nil
}

// Get returns a task by id.
func (q *Queue) Get(ctx context.Context, taskID string) (Task, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	var task Task
	err := q.pool.QueryRow(ctx, `
		SELECT id, organization_id, project_id, coalesce(run_id, ''), kind, status, payload,
		       attempt, max_attempts, coalesce(lease_owner, ''), coalesce(last_error, ''),
		       available_at, created_at, updated_at
		FROM task_queue WHERE id = $1`, taskID).
		Scan(&task.ID, &task.OrganizationID, &task.ProjectID, &task.RunID, &task.Kind,
			&task.Status, &task.Payload, &task.Attempt, &task.MaxAttempts, &task.LeaseOwner,
			&task.LastError, &task.AvailableAt, &task.CreatedAt, &task.UpdatedAt)
	if err != nil {
		if isNoRows(err) {
			return Task{}, fmt.Errorf("queue: task %s: %w", taskID, domain.ErrNotFound)
		}
		return Task{}, fmt.Errorf("queue: get: %w", err)
	}
	return task, nil
}

// CountByStatus returns task counts per status for an organization.
func (q *Queue) CountByStatus(ctx context.Context, organizationID string) (map[string]int, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := q.pool.Query(ctx, `
		SELECT status, count(*) FROM task_queue
		WHERE organization_id = $1 GROUP BY status`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("queue: count: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("queue: scan count: %w", err)
		}
		out[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("queue: iterate count: %w", err)
	}
	return out, nil
}

// RecoverExpiredLeases returns running tasks whose lease expired, which is how
// a crashed worker's work is found again.
func (q *Queue) RecoverExpiredLeases(ctx context.Context) ([]Task, error) {
	ctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	rows, err := q.pool.Query(ctx, `
		SELECT id, organization_id, project_id, coalesce(run_id, ''), kind, status, payload,
		       attempt, max_attempts, coalesce(lease_owner, ''), available_at, created_at, updated_at
		FROM task_queue
		WHERE status = 'running' AND lease_expires_at < now()
		ORDER BY available_at LIMIT 100`)
	if err != nil {
		return nil, fmt.Errorf("queue: recover: %w", err)
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		var task Task
		if err := rows.Scan(&task.ID, &task.OrganizationID, &task.ProjectID, &task.RunID,
			&task.Kind, &task.Status, &task.Payload, &task.Attempt, &task.MaxAttempts,
			&task.LeaseOwner, &task.AvailableAt, &task.CreatedAt, &task.UpdatedAt); err != nil {
			return nil, fmt.Errorf("queue: scan recover: %w", err)
		}
		out = append(out, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("queue: iterate recover: %w", err)
	}
	return out, nil
}

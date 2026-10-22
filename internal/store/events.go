// Package store extends the durable repositories with M15 run events, usage
// records and export metadata. Run events are append-only: every state change,
// step, tool call, test result, approval, PR action and recovery is recorded
// first, then streamed to clients over SSE.
package store

import (
	"context"
	"fmt"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/YASSERRMD/RepoSurgeon/internal/redact"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// eventColumns is the projection shared by every run-event read.
const eventColumns = `id, organization_id, project_id, run_id, actor_id, causation_id, sequence, type, step, payload, created_at`

// RunEvents is the append-only event log for one run. Rows are inserted, never
// updated or deleted; the database enforces immutability with a trigger.
type RunEvents struct {
	pool *pgxpool.Pool
}

// NewRunEvents returns a repository bound to the connection pool.
func NewRunEvents(pool *pgxpool.Pool) *RunEvents {
	return &RunEvents{pool: pool}
}

// scanRunEvent reads one row of the event projection.
func scanRunEvent(row pgx.CollectableRow) (domain.RunEvent, error) {
	var (
		event   domain.RunEvent
		actor   *string
		step    *string
		project *string
	)
	err := row.Scan(&event.ID, &event.OrganizationID, &project, &event.RunID, &actor,
		&event.CausationID, &event.Sequence, &event.Type, &step, &event.Payload, &event.CreatedAt)
	if err != nil {
		return domain.RunEvent{}, err
	}
	event.ActorID = derefString(actor)
	event.Step = derefString(step)
	event.ProjectID = derefString(project)
	return event, nil
}

// Create inserts one run event. The sequence is assigned by the database and
// returned on the event; payloads are redacted before insertion so secrets
// never reach the timeline.
func (r *RunEvents) Create(ctx context.Context, event domain.RunEvent) (domain.RunEvent, error) {
	if !nonEmpty(event.OrganizationID, event.RunID, event.CausationID, event.Type) {
		return domain.RunEvent{}, validationErrorf("run event", "organization_id, run_id, causation_id and type are required")
	}
	if event.ID == "" {
		event.ID = ids.MustNew("rev")
	}
	event.Payload = redact.Map(event.Payload)

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	err := r.pool.QueryRow(ctx, `
		INSERT INTO run_events (id, organization_id, project_id, run_id, actor_id, causation_id,
		                        type, step, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING sequence, created_at`,
		event.ID, event.OrganizationID, nilIfEmpty(event.ProjectID), event.RunID,
		nilIfEmpty(event.ActorID), event.CausationID, event.Type, nilIfEmpty(event.Step),
		event.Payload,
	).Scan(&event.Sequence, &event.CreatedAt)
	if err != nil {
		if isForeignKeyViolation(err) {
			return domain.RunEvent{}, fmt.Errorf("run event references an unknown organization, project, run or actor: %w", domain.ErrNotFound)
		}
		return domain.RunEvent{}, fmt.Errorf("create run event: %w", err)
	}
	return event, nil
}

// List returns a page of run events for one run, newest first, using keyset
// pagination on (sequence DESC, id DESC). The `after` cursor encodes the sort
// key of the last event from the previous page, so reconnecting clients resume
// without gaps or duplicates.
func (r *RunEvents) List(ctx context.Context, organizationID, runID string, limit int, cursor string) (Page[domain.RunEvent], error) {
	if !nonEmpty(organizationID, runID) {
		return Page[domain.RunEvent]{}, validationErrorf("run events", "organization_id and run_id are required")
	}
	limit = clampLimit(limit)

	var after any
	var afterID string
	if cursor != "" {
		sequence, id, err := ParseSequenceCursor(cursor)
		if err != nil {
			return Page[domain.RunEvent]{}, err
		}
		after, afterID = sequence, id
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	rows, err := r.pool.Query(ctx, `
		SELECT `+eventColumns+` FROM run_events
		  WHERE organization_id = $1 AND run_id = $2
		    AND ($3::bigint IS NULL OR (sequence, id) < ($3::bigint, $4))
		  ORDER BY sequence DESC, id DESC
		  LIMIT $5`,
		organizationID, runID, after, afterID, limit+1)
	if err != nil {
		return Page[domain.RunEvent]{}, fmt.Errorf("list run events for %s: %w", runID, err)
	}

	items, err := pgx.CollectRows(rows, scanRunEvent)
	if err != nil {
		return Page[domain.RunEvent]{}, fmt.Errorf("scan run events for %s: %w", runID, err)
	}

	return pageFromRows(items, limit, func(event domain.RunEvent) string {
		return SequenceCursor(event.Sequence, event.ID)
	}), nil
}

// ListRecent returns the newest events across an organization, optionally
// filtered by type, using keyset pagination. Tenant-scoped: callers never see
// another organization's events.
func (r *RunEvents) ListRecent(ctx context.Context, organizationID string, eventType string, limit int, cursor string) (Page[domain.RunEvent], error) {
	if !nonEmpty(organizationID) {
		return Page[domain.RunEvent]{}, validationErrorf("organization_id", "id is required")
	}
	limit = clampLimit(limit)

	var after any
	var afterID string
	if cursor != "" {
		sequence, id, err := ParseSequenceCursor(cursor)
		if err != nil {
			return Page[domain.RunEvent]{}, err
		}
		after, afterID = sequence, id
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	rows, err := r.pool.Query(ctx, `
		SELECT `+eventColumns+` FROM run_events
		  WHERE organization_id = $1
		    AND ($2 = '' OR type = $2)
		    AND ($3::bigint IS NULL OR (sequence, id) < ($3::bigint, $4))
		  ORDER BY sequence DESC, id DESC
		  LIMIT $5`,
		organizationID, eventType, after, afterID, limit+1)
	if err != nil {
		return Page[domain.RunEvent]{}, fmt.Errorf("list recent run events for org %s: %w", organizationID, err)
	}

	items, err := pgx.CollectRows(rows, scanRunEvent)
	if err != nil {
		return Page[domain.RunEvent]{}, fmt.Errorf("scan recent run events for org %s: %w", organizationID, err)
	}

	return pageFromRows(items, limit, func(event domain.RunEvent) string {
		return SequenceCursor(event.Sequence, event.ID)
	}), nil
}

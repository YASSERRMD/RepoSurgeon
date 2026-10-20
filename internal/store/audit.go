package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/YASSERRMD/RepoSurgeon/internal/redact"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// auditColumns is the projection shared by every audit read. The run_id column
// is intentionally not exposed: M01 audit events carry no run.
const auditColumns = `id, organization_id, project_id, actor_id, action, entity_type, entity_id, request_id, payload, sequence, created_at`

// auditInsertColumns counts the bound parameters of one audit row.
const auditInsertColumns = 9

// Audit is the append-only trail of state-changing requests. Rows are never
// updated or deleted, which the database enforces with a trigger.
type Audit struct {
	pool *pgxpool.Pool
}

// NewAudit returns a repository bound to the connection pool.
func NewAudit(pool *pgxpool.Pool) *Audit {
	return &Audit{pool: pool}
}

// scanAuditEvent reads one row of the audit projection, mapping the nullable
// project, actor and entity foreign keys onto empty strings.
func scanAuditEvent(row pgx.CollectableRow) (domain.AuditEvent, error) {
	var (
		event     domain.AuditEvent
		projectID *string
		actorID   *string
		entityID  *string
	)
	err := row.Scan(&event.ID, &event.OrganizationID, &projectID, &actorID, &event.Action,
		&event.EntityType, &entityID, &event.RequestID, &event.Payload, &event.Sequence, &event.CreatedAt)
	if err != nil {
		return domain.AuditEvent{}, err
	}
	event.ProjectID = derefString(projectID)
	event.ActorID = derefString(actorID)
	event.EntityID = derefString(entityID)
	return event, nil
}

// Append writes every event in one transaction, so a request either leaves a
// complete trail or none at all. Payloads are redacted before insertion and
// missing identifiers are generated.
func (r *Audit) Append(ctx context.Context, events []domain.AuditEvent) error {
	if len(events) == 0 {
		return validationErrorf("events", "at least one audit event is required")
	}

	prepared := make([]domain.AuditEvent, len(events))
	copy(prepared, events)
	for i := range prepared {
		event := prepared[i]
		if !nonEmpty(event.OrganizationID, event.Action, event.EntityType, event.RequestID) {
			return validationErrorf(fmt.Sprintf("events[%d]", i), "organization_id, action, entity_type and request_id are required")
		}
		if event.ID == "" {
			prepared[i].ID = ids.MustNew("aue")
		}
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin audit append: %w", err)
	}
	defer rollbackTx(ctx, tx)

	values := make([]string, 0, len(prepared))
	args := make([]any, 0, len(prepared)*auditInsertColumns)
	for i := range prepared {
		event := prepared[i]
		base := i * auditInsertColumns
		placeholders := make([]string, auditInsertColumns)
		for column := range placeholders {
			placeholders[column] = fmt.Sprintf("$%d", base+column+1)
		}
		values = append(values, "("+strings.Join(placeholders, ", ")+")")
		args = append(args,
			event.ID,
			event.OrganizationID,
			nilIfEmpty(event.ProjectID),
			nilIfEmpty(event.ActorID),
			event.Action,
			event.EntityType,
			nilIfEmpty(event.EntityID),
			event.RequestID,
			redact.Map(event.Payload),
		)
	}

	query := `INSERT INTO audit_events (id, organization_id, project_id, actor_id, action, entity_type, entity_id, request_id, payload)
	          VALUES ` + strings.Join(values, ", ")
	if _, err := tx.Exec(ctx, query, args...); err != nil {
		if isForeignKeyViolation(err) {
			return fmt.Errorf("audit event references an unknown organization, project or actor: %w", domain.ErrNotFound)
		}
		return fmt.Errorf("append audit events: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit audit append: %w", err)
	}
	return nil
}

// ListForProject returns a project's audit events, newest first, using keyset
// pagination on (sequence DESC, id DESC). An empty action filter matches every
// action. Events of other projects in the same organization are never
// returned, which keeps the listing tenant-scoped.
func (r *Audit) ListForProject(ctx context.Context, organizationID, projectID string, action string, limit int, cursor string) (Page[domain.AuditEvent], error) {
	if !nonEmpty(organizationID, projectID) {
		return Page[domain.AuditEvent]{}, validationErrorf("audit", "organization_id and project_id are required")
	}
	limit = clampLimit(limit)

	var after any
	var afterID string
	if cursor != "" {
		sequence, id, err := ParseSequenceCursor(cursor)
		if err != nil {
			return Page[domain.AuditEvent]{}, err
		}
		after, afterID = sequence, id
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	rows, err := r.pool.Query(ctx,
		`SELECT `+auditColumns+` FROM audit_events
		  WHERE organization_id = $1
		    AND project_id = $2
		    AND ($3 = '' OR action = $3)
		    AND ($4::bigint IS NULL OR (sequence, id) < ($4::bigint, $5))
		  ORDER BY sequence DESC, id DESC
		  LIMIT $6`,
		organizationID, projectID, action, after, afterID, limit+1)
	if err != nil {
		return Page[domain.AuditEvent]{}, fmt.Errorf("list audit events for project %s: %w", projectID, err)
	}

	items, err := pgx.CollectRows(rows, scanAuditEvent)
	if err != nil {
		return Page[domain.AuditEvent]{}, fmt.Errorf("scan audit events for project %s: %w", projectID, err)
	}

	return pageFromRows(items, limit, func(event domain.AuditEvent) string {
		return SequenceCursor(event.Sequence, event.ID)
	}), nil
}

// ListRecent returns the newest events across the organization, optionally
// filtered by action, using keyset pagination on (sequence DESC, id DESC).
func (r *Audit) ListRecent(ctx context.Context, organizationID string, action string, limit int, cursor string) (Page[domain.AuditEvent], error) {
	if !nonEmpty(organizationID) {
		return Page[domain.AuditEvent]{}, validationErrorf("organization_id", "id is required")
	}
	limit = clampLimit(limit)

	var after any
	var afterID string
	if cursor != "" {
		sequence, id, err := ParseSequenceCursor(cursor)
		if err != nil {
			return Page[domain.AuditEvent]{}, err
		}
		after, afterID = sequence, id
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	rows, err := r.pool.Query(ctx,
		`SELECT `+auditColumns+` FROM audit_events
		  WHERE organization_id = $1
		    AND ($2 = '' OR action = $2)
		    AND ($3::bigint IS NULL OR (sequence, id) < ($3::bigint, $4))
		  ORDER BY sequence DESC, id DESC
		  LIMIT $5`,
		organizationID, action, after, afterID, limit+1)
	if err != nil {
		return Page[domain.AuditEvent]{}, fmt.Errorf("list audit events for organization %s: %w", organizationID, err)
	}

	items, err := pgx.CollectRows(rows, scanAuditEvent)
	if err != nil {
		return Page[domain.AuditEvent]{}, fmt.Errorf("scan audit events for organization %s: %w", organizationID, err)
	}

	return pageFromRows(items, limit, func(event domain.AuditEvent) string {
		return SequenceCursor(event.Sequence, event.ID)
	}), nil
}

package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// projectColumns is the projection shared by every project read.
const projectColumns = `id, organization_id, name, description, settings, version, created_at, updated_at`

// Projects is the organization-scoped project repository. Every method takes
// the organization id explicitly so a project can never be read or written
// across the tenant boundary by guessing an identifier.
type Projects struct {
	pool *pgxpool.Pool
}

// NewProjects returns a repository bound to the connection pool.
func NewProjects(pool *pgxpool.Pool) *Projects {
	return &Projects{pool: pool}
}

// scanProject reads one row of the project projection.
func scanProject(row pgx.CollectableRow) (domain.Project, error) {
	var project domain.Project
	err := row.Scan(&project.ID, &project.OrganizationID, &project.Name, &project.Description,
		&project.Settings, &project.Version, &project.CreatedAt, &project.UpdatedAt)
	return project, err
}

// Create inserts a project after verifying that actorID holds a write role in
// the organization. A second live project with the same name in the same
// organization is a duplicate_name conflict.
func (r *Projects) Create(ctx context.Context, project domain.Project, actorID string) error {
	if !nonEmpty(project.ID, project.OrganizationID, project.Name) {
		return validationErrorf("project", "id, organization_id and name are required")
	}
	if !nonEmpty(actorID) {
		return validationErrorf("actor_id", "actor is required")
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	role, err := r.membershipRole(ctx, project.OrganizationID, actorID)
	if err != nil {
		return err
	}
	if !roleCanWrite(role) {
		return fmt.Errorf("%w: role %q cannot create projects in organization %s",
			domain.ErrPermissionDenied, role, project.OrganizationID)
	}

	createdAt := project.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := project.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}
	settings := project.Settings
	if settings == nil {
		settings = map[string]any{}
	}

	if _, err := r.pool.Exec(ctx,
		`INSERT INTO projects (id, organization_id, name, description, settings, version, created_by, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		project.ID, project.OrganizationID, project.Name, project.Description, settings,
		project.Version, nilIfEmpty(actorID), createdAt, updatedAt); err != nil {
		if isUniqueViolation(err) {
			return domain.NewDuplicateName("project", project.Name)
		}
		if isForeignKeyViolation(err) {
			return fmt.Errorf("organization %s: %w", project.OrganizationID, domain.ErrNotFound)
		}
		return fmt.Errorf("insert project: %w", err)
	}
	return nil
}

// GetForOrg returns a live project that belongs to organizationID. A project
// belonging to another organization is reported as not found rather than as
// forbidden, so the API never confirms that a foreign identifier exists.
func (r *Projects) GetForOrg(ctx context.Context, organizationID, projectID string) (domain.Project, error) {
	if !nonEmpty(organizationID, projectID) {
		return domain.Project{}, validationErrorf("project", "organization_id and project id are required")
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	rows, err := r.pool.Query(ctx,
		`SELECT `+projectColumns+` FROM projects
		 WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`, projectID, organizationID)
	if err != nil {
		return domain.Project{}, fmt.Errorf("get project %s: %w", projectID, err)
	}
	project, err := pgx.CollectOneRow(rows, scanProject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Project{}, fmt.Errorf("project %s in organization %s: %w", projectID, organizationID, domain.ErrNotFound)
		}
		return domain.Project{}, fmt.Errorf("scan project %s: %w", projectID, err)
	}
	return project, nil
}

// ListForOrg returns the organization's live projects, newest first, using
// keyset pagination on (created_at DESC, id DESC). The optional filter is a
// case-insensitive literal substring match on the project name.
func (r *Projects) ListForOrg(ctx context.Context, organizationID string, filter string, limit int, cursor string) (Page[domain.Project], error) {
	if !nonEmpty(organizationID) {
		return Page[domain.Project]{}, validationErrorf("organization_id", "id is required")
	}
	limit = clampLimit(limit)

	var after any
	var afterID string
	if cursor != "" {
		at, id, err := parseCreatedCursor(cursor)
		if err != nil {
			return Page[domain.Project]{}, err
		}
		after, afterID = at, id
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	rows, err := r.pool.Query(ctx,
		`SELECT `+projectColumns+` FROM projects
		  WHERE organization_id = $1
		    AND deleted_at IS NULL
		    AND ($2 = '' OR name ILIKE '%' || $3 || '%' ESCAPE '\')
		    AND ($4::timestamptz IS NULL OR (created_at, id) < ($4::timestamptz, $5))
		  ORDER BY created_at DESC, id DESC
		  LIMIT $6`,
		organizationID, filter, likeEscape(filter), after, afterID, limit+1)
	if err != nil {
		return Page[domain.Project]{}, fmt.Errorf("list projects in organization %s: %w", organizationID, err)
	}

	items, err := pgx.CollectRows(rows, scanProject)
	if err != nil {
		return Page[domain.Project]{}, fmt.Errorf("scan projects in organization %s: %w", organizationID, err)
	}

	return pageFromRows(items, limit, func(project domain.Project) string {
		return createdCursor(project.CreatedAt, project.ID)
	}), nil
}

// UpdateForOrg applies a partial update guarded by version, the caller's
// optimistic concurrency token, and records who made the change. Only the
// fields present in update are written; the stored version is always
// incremented. When settings change, the new settings are recorded in
// project_settings_revisions inside the same transaction. actorID may be empty
// when the caller has no authenticated principal, which leaves the revision
// unattributed.
func (r *Projects) UpdateForOrg(ctx context.Context, organizationID, projectID string, version int64, update domain.ProjectUpdate, actorID string) (domain.Project, error) {
	if !nonEmpty(organizationID, projectID) {
		return domain.Project{}, validationErrorf("project", "organization_id and project id are required")
	}
	if version < 0 {
		return domain.Project{}, validationErrorf("version", "version must not be negative")
	}

	assignments := make([]string, 0, 5)
	args := []any{projectID, organizationID, version}
	if update.Name != nil {
		if !nonEmpty(*update.Name) {
			return domain.Project{}, validationErrorf("name", "name must not be empty")
		}
		args = append(args, *update.Name)
		assignments = append(assignments, fmt.Sprintf("name = $%d", len(args)))
	}
	if update.Description != nil {
		args = append(args, *update.Description)
		assignments = append(assignments, fmt.Sprintf("description = $%d", len(args)))
	}
	settingsChanged := update.Settings != nil
	if settingsChanged {
		settings := update.Settings
		if settings == nil {
			settings = map[string]any{}
		}
		args = append(args, settings)
		assignments = append(assignments, fmt.Sprintf("settings = $%d", len(args)))
	}
	assignments = append(assignments, "version = version + 1", "updated_at = now()")

	query := `UPDATE projects SET ` + strings.Join(assignments, ", ") +
		` WHERE id = $1 AND organization_id = $2 AND version = $3 AND deleted_at IS NULL
		  RETURNING ` + projectColumns

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return domain.Project{}, fmt.Errorf("begin project update: %w", err)
	}
	defer rollbackTx(ctx, tx)

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return domain.Project{}, fmt.Errorf("update project %s: %w", projectID, err)
	}
	project, err := pgx.CollectOneRow(rows, scanProject)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Project{}, r.conflictOrNotFound(ctx, organizationID, projectID)
		}
		if isUniqueViolation(err) {
			if update.Name != nil {
				return domain.Project{}, domain.NewDuplicateName("project", *update.Name)
			}
			return domain.Project{}, fmt.Errorf("update project %s: %w", projectID, err)
		}
		return domain.Project{}, fmt.Errorf("update project %s: %w", projectID, err)
	}

	if settingsChanged {
		if _, err := tx.Exec(ctx,
			`INSERT INTO project_settings_revisions (id, project_id, version, settings, changed_by)
			 VALUES ($1, $2, $3, $4, $5)`,
			ids.MustNew("psr"), project.ID, project.Version, project.Settings, nilIfEmpty(actorID)); err != nil {
			return domain.Project{}, fmt.Errorf("record project settings revision: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Project{}, fmt.Errorf("commit project update: %w", err)
	}
	return project, nil
}

// conflictOrNotFound re-reads a project after a no-op update to tell a missing
// project from a stale version, which the caller must be able to distinguish.
func (r *Projects) conflictOrNotFound(ctx context.Context, organizationID, projectID string) error {
	var current int64
	err := r.pool.QueryRow(ctx,
		`SELECT version FROM projects
		 WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL`, projectID, organizationID).Scan(&current)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("project %s in organization %s: %w", projectID, organizationID, domain.ErrNotFound)
		}
		return fmt.Errorf("re-read project %s: %w", projectID, err)
	}
	return domain.NewVersionConflict(current)
}

// membershipRole returns the actor's role in an organization. A user who is
// not a member has no role at all, which the write check reports as a
// permission failure.
func (r *Projects) membershipRole(ctx context.Context, organizationID, userID string) (string, error) {
	var role string
	err := r.pool.QueryRow(ctx,
		`SELECT role FROM memberships WHERE organization_id = $1 AND user_id = $2`,
		organizationID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: user %s is not a member of organization %s",
				domain.ErrPermissionDenied, userID, organizationID)
		}
		return "", fmt.Errorf("look up membership in organization %s: %w", organizationID, err)
	}
	return role, nil
}

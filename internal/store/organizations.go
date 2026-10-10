package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// organizationColumns is the projection shared by every organization read so
// no query can accidentally expose soft-deleted rows or internal columns.
const organizationColumns = `id, slug, name, created_at, updated_at`

// Organizations is the tenant-boundary repository. Every method is scoped by
// organization id or user id; there is no unscoped listing.
type Organizations struct {
	pool *pgxpool.Pool
}

// NewOrganizations returns a repository bound to the connection pool.
func NewOrganizations(pool *pgxpool.Pool) *Organizations {
	return &Organizations{pool: pool}
}

// Create inserts an organization together with the first membership of
// actorID in a single transaction, so an organization can never exist without
// its owner. Either failure rolls both statements back.
func (r *Organizations) Create(ctx context.Context, org domain.Organization, actorID, role string) error {
	if !nonEmpty(org.ID, org.Slug, org.Name) {
		return validationErrorf("organization", "id, slug and name are required")
	}
	if !nonEmpty(actorID) {
		return validationErrorf("actor_id", "actor is required")
	}
	if !isKnownRole(role) {
		return validationErrorf("role", "%q is not a recognized membership role", role)
	}

	createdAt := org.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	updatedAt := org.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin organization create: %w", err)
	}
	defer rollbackTx(ctx, tx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO organizations (id, slug, name, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		org.ID, org.Slug, org.Name, createdAt, updatedAt); err != nil {
		if isUniqueViolation(err) {
			return domain.NewDuplicateName("organization", org.Name)
		}
		return fmt.Errorf("insert organization: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO memberships (id, organization_id, user_id, role)
		 VALUES ($1, $2, $3, $4)`,
		ids.MustNew("mbr"), org.ID, actorID, role); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: actor %s is already a member of organization %s", domain.ErrPermissionDenied, actorID, org.ID)
		}
		if isCheckViolation(err) {
			return validationErrorf("role", "%q is not a recognized membership role", role)
		}
		return fmt.Errorf("insert founding membership: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit organization create: %w", err)
	}
	return nil
}

// Get returns a live organization. Soft-deleted organizations are invisible.
func (r *Organizations) Get(ctx context.Context, organizationID string) (domain.Organization, error) {
	if !nonEmpty(organizationID) {
		return domain.Organization{}, validationErrorf("organization_id", "id is required")
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	var org domain.Organization
	err := r.pool.QueryRow(ctx,
		`SELECT `+organizationColumns+` FROM organizations
		 WHERE id = $1 AND deleted_at IS NULL`, organizationID).
		Scan(&org.ID, &org.Slug, &org.Name, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Organization{}, fmt.Errorf("organization %s: %w", organizationID, domain.ErrNotFound)
		}
		return domain.Organization{}, fmt.Errorf("get organization %s: %w", organizationID, err)
	}
	return org, nil
}

// GetBySlug resolves a live organization by its globally unique slug.
func (r *Organizations) GetBySlug(ctx context.Context, slug string) (domain.Organization, error) {
	if !nonEmpty(slug) {
		return domain.Organization{}, validationErrorf("slug", "slug is required")
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	var org domain.Organization
	err := r.pool.QueryRow(ctx,
		`SELECT `+organizationColumns+` FROM organizations
		 WHERE slug = $1 AND deleted_at IS NULL`, slug).
		Scan(&org.ID, &org.Slug, &org.Name, &org.CreatedAt, &org.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Organization{}, fmt.Errorf("organization %s: %w", slug, domain.ErrNotFound)
		}
		return domain.Organization{}, fmt.Errorf("get organization by slug %s: %w", slug, err)
	}
	return org, nil
}

// ListForUser returns the organizations the user belongs to, newest first,
// using keyset pagination on (created_at DESC, id DESC). A malformed cursor is
// a client error.
func (r *Organizations) ListForUser(ctx context.Context, userID string, limit int, cursor string) (Page[domain.OrganizationWithRole], error) {
	if !nonEmpty(userID) {
		return Page[domain.OrganizationWithRole]{}, validationErrorf("user_id", "id is required")
	}
	limit = clampLimit(limit)

	var after any
	var afterID string
	if cursor != "" {
		at, id, err := parseCreatedCursor(cursor)
		if err != nil {
			return Page[domain.OrganizationWithRole]{}, err
		}
		after, afterID = at, id
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	rows, err := r.pool.Query(ctx,
		`SELECT o.id, o.slug, o.name, o.created_at, o.updated_at, m.role
		   FROM organizations o
		   JOIN memberships m ON m.organization_id = o.id
		  WHERE m.user_id = $1
		    AND o.deleted_at IS NULL
		    AND ($2::timestamptz IS NULL OR (o.created_at, o.id) < ($2::timestamptz, $3))
		  ORDER BY o.created_at DESC, o.id DESC
		  LIMIT $4`,
		userID, after, afterID, limit+1)
	if err != nil {
		return Page[domain.OrganizationWithRole]{}, fmt.Errorf("list organizations for user %s: %w", userID, err)
	}

	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.OrganizationWithRole, error) {
		var item domain.OrganizationWithRole
		err := row.Scan(&item.ID, &item.Slug, &item.Name, &item.CreatedAt, &item.UpdatedAt, &item.Role)
		return item, err
	})
	if err != nil {
		return Page[domain.OrganizationWithRole]{}, fmt.Errorf("scan organizations for user %s: %w", userID, err)
	}

	return pageFromRows(items, limit, func(item domain.OrganizationWithRole) string {
		return createdCursor(item.CreatedAt, item.ID)
	}), nil
}

// Membership returns the caller's role in an organization. Non-members, and
// members of a soft-deleted organization, are reported as not found.
func (r *Organizations) Membership(ctx context.Context, organizationID, userID string) (string, error) {
	if !nonEmpty(organizationID, userID) {
		return "", validationErrorf("membership", "organization_id and user_id are required")
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	var role string
	err := r.pool.QueryRow(ctx,
		`SELECT m.role
		   FROM memberships m
		   JOIN organizations o ON o.id = m.organization_id
		  WHERE m.organization_id = $1 AND m.user_id = $2 AND o.deleted_at IS NULL`,
		organizationID, userID).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("membership of user %s in organization %s: %w", userID, organizationID, domain.ErrNotFound)
		}
		return "", fmt.Errorf("get membership: %w", err)
	}
	return role, nil
}

// SlugTaken reports whether slug is already used, including by a soft-deleted
// organization, because the unique index is not partial on that column and a
// second live organization can never reuse it.
func (r *Organizations) SlugTaken(ctx context.Context, slug string) (bool, error) {
	if !nonEmpty(slug) {
		return false, validationErrorf("slug", "slug is required")
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	var taken bool
	if err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM organizations WHERE slug = $1)`, slug).Scan(&taken); err != nil {
		return false, fmt.Errorf("check slug %s: %w", slug, err)
	}
	return taken, nil
}

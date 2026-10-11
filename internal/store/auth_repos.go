package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewSessions binds the M02 session repository to the pool.
func NewSessions(pool *pgxpool.Pool) *Sessions { return &Sessions{pool: pool} }

// NewIdentities binds the M02 identity repository to the pool.
func NewIdentities(pool *pgxpool.Pool) *Identities { return &Identities{pool: pool} }

// NewCredentials binds the M02 credential repository to the pool.
func NewCredentials(pool *pgxpool.Pool) *Credentials { return &Credentials{pool: pool} }

// Member is an organization membership with the user's identity.
type Member struct {
	ID          string
	UserID      string
	Email       string
	DisplayName string
	Role        string
	CreatedAt   time.Time
}

// Members manages memberships and resolves role permissions.
type Members struct {
	pool *pgxpool.Pool
}

// NewMembers binds the membership repository to the pool.
func NewMembers(pool *pgxpool.Pool) *Members { return &Members{pool: pool} }

// List returns an organization's members ordered by join date.
func (r *Members) List(ctx context.Context, organizationID string, limit int) (Page[Member], error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	rows, err := r.pool.Query(ctx, `
		SELECT m.id, m.user_id, u.email, u.display_name, m.role, m.created_at
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1
		ORDER BY m.created_at, m.id LIMIT $2`,
		organizationID, int64(clampLimit(limit)))
	if err != nil {
		return Page[Member]{}, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()
	var items []Member
	for rows.Next() {
		var member Member
		if err := rows.Scan(&member.ID, &member.UserID, &member.Email, &member.DisplayName, &member.Role, &member.CreatedAt); err != nil {
			return Page[Member]{}, fmt.Errorf("scan member: %w", err)
		}
		items = append(items, member)
	}
	if err := rows.Err(); err != nil {
		return Page[Member]{}, fmt.Errorf("iterate members: %w", err)
	}
	return Page[Member]{Items: items}, nil
}

// CountOwners counts owners of an organization; used to protect the last one.
func (r *Members) CountOwners(ctx context.Context, organizationID string) (int, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	var count int
	if err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM memberships
		WHERE organization_id = $1 AND role = 'owner'`, organizationID).Scan(&count); err != nil {
		return 0, fmt.Errorf("count owners: %w", err)
	}
	return count, nil
}

// Get returns one membership.
func (r *Members) Get(ctx context.Context, organizationID, membershipID string) (Member, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	var member Member
	err := r.pool.QueryRow(ctx, `
		SELECT m.id, m.user_id, u.email, u.display_name, m.role, m.created_at
		FROM memberships m
		JOIN users u ON u.id = m.user_id
		WHERE m.organization_id = $1 AND m.id = $2`,
		organizationID, membershipID).
		Scan(&member.ID, &member.UserID, &member.Email, &member.DisplayName, &member.Role, &member.CreatedAt)
	if err != nil {
		if errorsIsNoRows(err) {
			return Member{}, fmt.Errorf("membership: %w", domain.ErrNotFound)
		}
		return Member{}, fmt.Errorf("get member: %w", err)
	}
	return member, nil
}

// Invite adds or updates a membership for an email, creating the user when
// needed.
func (r *Members) Invite(ctx context.Context, organizationID, email, role, displayName string) (Member, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Member{}, fmt.Errorf("begin invite: %w", err)
	}
	defer rollbackTx(ctx, tx)

	var userID string
	err = tx.QueryRow(ctx, `
		INSERT INTO users (id, email, display_name)
		VALUES (gen_random_uuid()::text, $1, $2)
		ON CONFLICT (lower(email)) WHERE deleted_at IS NULL
		DO UPDATE SET email = excluded.email
		RETURNING id`, email, displayName).Scan(&userID)
	if err != nil {
		return Member{}, fmt.Errorf("upsert user: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO memberships (id, organization_id, user_id, role)
		VALUES (gen_random_uuid()::text, $1, $2, $3)
		ON CONFLICT (organization_id, user_id) DO UPDATE SET role = excluded.role`,
		organizationID, userID, role); err != nil {
		return Member{}, fmt.Errorf("upsert membership: %w", err)
	}

	var membershipID string
	if err := tx.QueryRow(ctx, `
		SELECT id FROM memberships WHERE organization_id = $1 AND user_id = $2`,
		organizationID, userID).Scan(&membershipID); err != nil {
		return Member{}, fmt.Errorf("read membership: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Member{}, fmt.Errorf("commit invite: %w", err)
	}
	return r.Get(ctx, organizationID, membershipID)
}

// UpdateRole changes a membership's role.
func (r *Members) UpdateRole(ctx context.Context, organizationID, membershipID, role string) (Member, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	if _, err := r.pool.Exec(ctx, `
		UPDATE memberships SET role = $3, updated_at = now()
		WHERE organization_id = $1 AND id = $2`, organizationID, membershipID, role); err != nil {
		return Member{}, fmt.Errorf("update role: %w", err)
	}
	return r.Get(ctx, organizationID, membershipID)
}

// Remove deletes a membership.
func (r *Members) Remove(ctx context.Context, organizationID, membershipID string) error {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	tag, err := r.pool.Exec(ctx, `
		DELETE FROM memberships WHERE organization_id = $1 AND id = $2`,
		organizationID, membershipID)
	if err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("membership %s: %w", membershipID, domain.ErrNotFound)
	}
	return nil
}

// PermissionsFor returns the permission keys granted to a user in an
// organization, resolved through the role_permissions table.
func (r *Members) PermissionsFor(ctx context.Context, organizationID, userID string) ([]string, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	rows, err := r.pool.Query(ctx, `
		SELECT rp.permission_key
		FROM memberships m
		JOIN role_permissions rp ON rp.role = m.role
		WHERE m.organization_id = $1 AND m.user_id = $2
		ORDER BY rp.permission_key`, organizationID, userID)
	if err != nil {
		return nil, fmt.Errorf("permissions: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan permission: %w", err)
		}
		out = append(out, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate permissions: %w", err)
	}
	return out, nil
}

// RoleFor returns the user's role in an organization.
func (r *Members) RoleFor(ctx context.Context, organizationID, userID string) (string, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	var role string
	err := r.pool.QueryRow(ctx, `
		SELECT role FROM memberships WHERE organization_id = $1 AND user_id = $2`,
		organizationID, userID).Scan(&role)
	if err != nil {
		if errorsIsNoRows(err) {
			return "", fmt.Errorf("membership: %w", domain.ErrNotFound)
		}
		return "", fmt.Errorf("role: %w", err)
	}
	return role, nil
}

func errorsIsNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

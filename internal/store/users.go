package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Users is the principal repository. Email identity is case-insensitive and
// unique among live users, enforced by users_email_unique_idx.
type Users struct {
	pool *pgxpool.Pool
}

// NewUsers returns a repository bound to the connection pool.
func NewUsers(pool *pgxpool.Pool) *Users {
	return &Users{pool: pool}
}

// UpsertByEmail returns the id of the user owning email, creating the user on
// first sight and refreshing the display name afterwards. Emails are matched
// case-insensitively, so a login with different casing resolves to the same
// principal, and a revived principal is no longer soft-deleted.
func (r *Users) UpsertByEmail(ctx context.Context, email, displayName string) (string, error) {
	normalized, err := normalizeEmail(email)
	if err != nil {
		return "", err
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	var userID string
	err = r.pool.QueryRow(ctx,
		`INSERT INTO users (id, email, display_name)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (lower(email)) WHERE deleted_at IS NULL
		 DO UPDATE SET display_name = EXCLUDED.display_name, deleted_at = NULL, updated_at = now()
		 RETURNING id`,
		ids.MustNew("usr"), normalized, displayName).Scan(&userID)
	if err != nil {
		return "", fmt.Errorf("upsert user %s: %w", normalized, err)
	}
	return userID, nil
}

// Get returns a live user. Soft-deleted users are invisible so a deleted
// principal cannot be addressed again.
func (r *Users) Get(ctx context.Context, userID string) (domain.User, error) {
	if !nonEmpty(userID) {
		return domain.User{}, validationErrorf("user_id", "id is required")
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	var user domain.User
	err := r.pool.QueryRow(ctx,
		`SELECT id, email, display_name, created_at, updated_at FROM users
		  WHERE id = $1 AND deleted_at IS NULL`, userID).
		Scan(&user.ID, &user.Email, &user.DisplayName, &user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, fmt.Errorf("user %s: %w", userID, domain.ErrNotFound)
		}
		return domain.User{}, fmt.Errorf("get user %s: %w", userID, err)
	}
	return user, nil
}

// normalizeEmail trims and lowercases an email address and rejects addresses
// that cannot identify a principal.
func normalizeEmail(email string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return "", validationErrorf("email", "email is required")
	}
	if !strings.Contains(normalized, "@") {
		return "", validationErrorf("email", "%q is not an email address", email)
	}
	return normalized, nil
}

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

// GitHubInstallation binds a GitHub App installation to an organization.
type GitHubInstallation struct {
	ID             string
	OrganizationID string
	InstallationID int64
	AccountLogin   string
	AppSlug        string
	Status         string
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// GitHubInstallations manages installation bindings.
type GitHubInstallations struct {
	pool *pgxpool.Pool
}

// NewGitHubInstallations binds the repository to the pool.
func NewGitHubInstallations(pool *pgxpool.Pool) *GitHubInstallations {
	return &GitHubInstallations{pool: pool}
}

// Connect binds an installation to an organization, replacing any previous
// binding for the same installation id.
func (r *GitHubInstallations) Connect(ctx context.Context, binding GitHubInstallation) error {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	_, err := r.pool.Exec(ctx, `
		INSERT INTO github_installations
			(id, organization_id, installation_id, account_login, app_slug, created_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (organization_id, installation_id) DO UPDATE SET
			account_login = excluded.account_login,
			app_slug = excluded.app_slug,
			status = 'active',
			updated_at = now()`,
		binding.ID, binding.OrganizationID, binding.InstallationID,
		binding.AccountLogin, binding.AppSlug, binding.CreatedBy)
	if err != nil {
		return fmt.Errorf("connect installation: %w", err)
	}
	return nil
}

// List returns an organization's installations.
func (r *GitHubInstallations) List(ctx context.Context, organizationID string) ([]GitHubInstallation, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, installation_id, account_login, app_slug, status,
		       coalesce(created_by, ''), created_at, updated_at
		FROM github_installations
		WHERE organization_id = $1 AND status = 'active'
		ORDER BY created_at`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("list installations: %w", err)
	}
	defer rows.Close()
	var out []GitHubInstallation
	for rows.Next() {
		var installation GitHubInstallation
		if err := rows.Scan(&installation.ID, &installation.OrganizationID, &installation.InstallationID,
			&installation.AccountLogin, &installation.AppSlug, &installation.Status,
			&installation.CreatedBy, &installation.CreatedAt, &installation.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan installation: %w", err)
		}
		out = append(out, installation)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate installations: %w", err)
	}
	return out, nil
}

// Get resolves an installation binding for an organization.
func (r *GitHubInstallations) Get(ctx context.Context, organizationID string, installationID int64) (GitHubInstallation, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	var installation GitHubInstallation
	err := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, installation_id, account_login, app_slug, status,
		       coalesce(created_by, ''), created_at, updated_at
		FROM github_installations
		WHERE organization_id = $1 AND installation_id = $2`, organizationID, installationID).
		Scan(&installation.ID, &installation.OrganizationID, &installation.InstallationID,
			&installation.AccountLogin, &installation.AppSlug, &installation.Status,
			&installation.CreatedBy, &installation.CreatedAt, &installation.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return GitHubInstallation{}, fmt.Errorf("installation %d: %w", installationID, domain.ErrNotFound)
		}
		return GitHubInstallation{}, fmt.Errorf("get installation: %w", err)
	}
	return installation, nil
}

// MarkRemoved flags an installation as removed so it can no longer be used.
func (r *GitHubInstallations) MarkRemoved(ctx context.Context, organizationID string, installationID int64) error {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	_, err := r.pool.Exec(ctx, `
		UPDATE github_installations SET status = 'removed', updated_at = now()
		WHERE organization_id = $1 AND installation_id = $2`, organizationID, installationID)
	if err != nil {
		return fmt.Errorf("mark removed: %w", err)
	}
	return nil
}

// WebhookDelivery records a processed delivery so replays are ignored.
type WebhookDelivery struct {
	ID             string
	InstallationID int64
	EventType      string
	Action         string
	PayloadHash    string
	ProcessedAt    time.Time
}

// WebhookDeliveries de-duplicates GitHub webhook deliveries.
type WebhookDeliveries struct {
	pool *pgxpool.Pool
}

// NewWebhookDeliveries binds the repository to the pool.
func NewWebhookDeliveries(pool *pgxpool.Pool) *WebhookDeliveries {
	return &WebhookDeliveries{pool: pool}
}

// Record inserts a delivery; it is a no-op when the ID was already stored.
func (r *WebhookDeliveries) Record(ctx context.Context, delivery WebhookDelivery) (alreadySeen bool, err error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	tag, err := r.pool.Exec(ctx, `
		INSERT INTO webhook_deliveries (id, installation_id, event_type, action, payload_hash)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO NOTHING`,
		delivery.ID, delivery.InstallationID, delivery.EventType, delivery.Action, delivery.PayloadHash)
	if err != nil {
		return false, fmt.Errorf("record delivery: %w", err)
	}
	return tag.RowsAffected() == 0, nil
}

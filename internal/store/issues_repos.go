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

// Issues manages imported issues for a project.
type Issues struct {
	pool *pgxpool.Pool
}

// NewIssues binds the repository to the pool.
func NewIssues(pool *pgxpool.Pool) *Issues { return &Issues{pool: pool} }

// IssueRecord is the stored representation of an imported issue.
type IssueRecord struct {
	domain.Issue
	ImportedBy string    `json:"imported_by,omitempty"`
	SyncCursor time.Time `json:"sync_cursor"`
}

// Import inserts or updates an issue, keyed by repository and external id. A
// body change updates body_hash so downstream runs can detect staleness.
func (r *Issues) Import(ctx context.Context, record IssueRecord) (IssueRecord, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	if record.ID == "" {
		record.ID = domain.NewIssueID()
	}
	if record.BodyHash == "" {
		record.BodyHash = domain.HashBody(record.Body)
	}
	err := r.pool.QueryRow(ctx, `
		INSERT INTO issues (id, organization_id, project_id, repository_id, external_id, number,
		                    title, body, state, body_hash, etag, labels, assignees, comments_count,
		                    imported_by, last_synced_at, sync_cursor)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, now(), now())
		ON CONFLICT (repository_id, external_id) WHERE repository_id IS NOT NULL AND external_id IS NOT NULL
		DO UPDATE SET
			title = excluded.title,
			body = excluded.body,
			state = excluded.state,
			body_hash = excluded.body_hash,
			etag = excluded.etag,
			labels = excluded.labels,
			assignees = excluded.assignees,
			comments_count = excluded.comments_count,
			last_synced_at = now(),
			sync_cursor = excluded.sync_cursor
		RETURNING id`,
		record.ID, record.OrganizationID, record.ProjectID, record.RepositoryID,
		fmt.Sprint(record.ExternalID), record.Number, record.Title, record.Body, record.State,
		record.BodyHash, record.ETag, record.Labels, record.Assignees,
		record.CommentsCount, record.ImportedBy).Scan(&record.ID)
	if err != nil {
		return IssueRecord{}, fmt.Errorf("import issue: %w", err)
	}
	return record, nil
}

// ListForProject returns a project's issues, newest first.
func (r *Issues) ListForProject(ctx context.Context, projectID string, limit int, cursor string) (Page[IssueRecord], error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	rows, err := r.pool.Query(ctx, `
		SELECT id, organization_id, project_id, coalesce(repository_id, ''), coalesce(external_id, ''),
		       number, title, body, state, body_hash, coalesce(etag, ''), labels, assignees,
		       comments_count, created_at, updated_at
		FROM issues
		WHERE project_id = $1
		ORDER BY created_at DESC, id DESC LIMIT $2`,
		projectID, int64(clampLimit(limit)+1))
	if err != nil {
		return Page[IssueRecord]{}, fmt.Errorf("list issues: %w", err)
	}
	defer rows.Close()
	var items []IssueRecord
	for rows.Next() {
		record, err := scanIssueRecord(rows)
		if err != nil {
			return Page[IssueRecord]{}, err
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return Page[IssueRecord]{}, fmt.Errorf("iterate issues: %w", err)
	}
	return pageFromRows(items, clampLimit(limit), func(record IssueRecord) string {
		return createdCursor(record.CreatedAt, record.ID)
	}), nil
}

// GetForProject returns one issue in a project.
func (r *Issues) GetForProject(ctx context.Context, projectID, issueID string) (IssueRecord, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	row := r.pool.QueryRow(ctx, `
		SELECT id, organization_id, project_id, coalesce(repository_id, ''), coalesce(external_id, ''),
		       number, title, body, state, body_hash, coalesce(etag, ''), labels, assignees,
		       comments_count, created_at, updated_at
		FROM issues
		WHERE project_id = $1 AND id = $2`, projectID, issueID)
	record, err := scanIssueRecord(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IssueRecord{}, fmt.Errorf("issue %s: %w", issueID, domain.ErrNotFound)
		}
		return IssueRecord{}, fmt.Errorf("get issue: %w", err)
	}
	return record, nil
}

func scanIssueRecord(row scannable) (IssueRecord, error) {
	var record IssueRecord
	err := row.Scan(
		&record.ID, &record.OrganizationID, &record.ProjectID, &record.RepositoryID,
		&record.ExternalID, &record.Number, &record.Title, &record.Body, &record.State,
		&record.BodyHash, &record.ETag, &record.Labels, &record.Assignees,
		&record.CommentsCount, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		return IssueRecord{}, err
	}
	return record, nil
}

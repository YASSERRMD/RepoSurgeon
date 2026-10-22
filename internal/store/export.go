// Package store extends the durable repositories with M15 export records.
// Export records track requested trace exports so clients can poll for a
// ready document.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ExportRecord is one requested trace export.
type ExportRecord struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	RunID          string    `json:"run_id"`
	Format         string    `json:"format"` // json, markdown
	Status         string    `json:"status"` // pending, ready, failed
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
}

// ExportRecords owns the export metadata table.
type ExportRecords struct {
	pool *pgxpool.Pool
}

// NewExportRecords returns a repository bound to the connection pool.
func NewExportRecords(pool *pgxpool.Pool) *ExportRecords {
	return &ExportRecords{pool: pool}
}

// Create inserts a new pending export record.
func (e *ExportRecords) Create(ctx context.Context, organizationID, runID, format string, expiresAt time.Time) (ExportRecord, error) {
	if !nonEmpty(organizationID, runID, format) {
		return ExportRecord{}, validationErrorf("export record", "organization_id, run_id and format are required")
	}
	record := ExportRecord{
		ID:             ids.MustNew("exp"),
		OrganizationID: organizationID,
		RunID:          runID,
		Format:         format,
		Status:         "pending",
		ExpiresAt:      expiresAt,
		CreatedAt:      time.Now().UTC(),
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	err := e.pool.QueryRow(ctx, `
		INSERT INTO export_records (id, organization_id, run_id, format, status, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at`,
		record.ID, record.OrganizationID, record.RunID, record.Format, record.Status,
		record.ExpiresAt, record.CreatedAt,
	).Scan(&record.CreatedAt)
	if err != nil {
		if isForeignKeyViolation(err) {
			return ExportRecord{}, fmt.Errorf("export record references an unknown organization or run: %w", domain.ErrNotFound)
		}
		return ExportRecord{}, fmt.Errorf("create export record: %w", err)
	}
	return record, nil
}

// GetByRun returns the latest export record for a run.
func (e *ExportRecords) GetByRun(ctx context.Context, organizationID, runID string) (ExportRecord, bool, error) {
	if !nonEmpty(organizationID, runID) {
		return ExportRecord{}, false, validationErrorf("export record", "organization_id and run_id are required")
	}

	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	var record ExportRecord
	err := e.pool.QueryRow(ctx, `
		SELECT id, organization_id, run_id, format, status, expires_at, created_at
		  FROM export_records
		 WHERE organization_id = $1 AND run_id = $2
		 ORDER BY created_at DESC
		 LIMIT 1`,
		organizationID, runID,
	).Scan(&record.ID, &record.OrganizationID, &record.RunID, &record.Format, &record.Status,
		&record.ExpiresAt, &record.CreatedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return ExportRecord{}, false, nil
		}
		return ExportRecord{}, false, fmt.Errorf("get export record for run %s: %w", runID, err)
	}
	return record, true, nil
}

// UpdateStatus marks an export record as ready or failed.
func (e *ExportRecords) UpdateStatus(ctx context.Context, id, status string) error {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	_, err := e.pool.Exec(ctx, `UPDATE export_records SET status = $1 WHERE id = $2`, status, id)
	if err != nil {
		return fmt.Errorf("update export record %s: %w", id, err)
	}
	return nil
}

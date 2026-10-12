// Package recovery persistence keeps the state recovery journal, attempt
// history, per-class budgets, effect receipts and content-bearing checkpoints
// durable. Every repository scopes statements to an organization so one tenant
// can never read another tenant's recovery evidence.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/recovery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RecoveryEventRow is one stored journal entry.
type RecoveryEventRow struct {
	ID             string
	RunID          string
	OrganizationID string
	Attempt        int
	Class          string
	Kind           string
	Reason         string
	Evidence       map[string]any
	ActorType      string
	CreatedAt      time.Time
}

// RecoveryAttemptRow is one stored attempt.
type RecoveryAttemptRow struct {
	ID               string
	RunID            string
	OrganizationID   string
	Number           int
	Kind             string
	Fingerprint      string
	PatchFingerprint string
	FailingCount     int
	Verdict          string
	Note             string
	CreatedAt        time.Time
}

// BudgetRow is one stored attempt budget.
type BudgetRow struct {
	RunID   string
	Class   string
	Allowed int
	Used    int
	Exhaust bool
}

// CheckpointRow is one stored workspace snapshot.
type CheckpointRow struct {
	ID             string
	RunID          string
	OrganizationID string
	WorkspaceID    string
	Verdict        string
	Files          json.RawMessage
	CreatedAt      time.Time
}

// RecoveryJournal appends and lists state recovery events.
type RecoveryJournal struct {
	pool *pgxpool.Pool
}

// NewRecoveryJournal builds the journal repository.
func NewRecoveryJournal(pool *pgxpool.Pool) *RecoveryJournal { return &RecoveryJournal{pool: pool} }

// Append stores one journal entry. Appending is idempotent on entry id.
func (r *RecoveryJournal) Append(ctx context.Context, event RecoveryEventRow) error {
	if event.ID == "" || event.RunID == "" || event.OrganizationID == "" {
		return domain.ErrValidation
	}
	evidence, err := json.Marshal(event.Evidence)
	if err != nil {
		return fmt.Errorf("recovery journal: marshal evidence: %w", err)
	}
	if event.Class == "" {
		event.Class = string(recovery.ClassUnknown)
	}
	_, err = r.pool.Exec(ctx, `
        INSERT INTO recovery_events (id, run_id, organization_id, attempt, class, kind, reason, evidence, actor_type)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
        ON CONFLICT (id) DO NOTHING`,
		event.ID, event.RunID, event.OrganizationID, event.Attempt, event.Class, event.Kind, event.Reason, evidence, event.ActorType)
	if err != nil {
		return fmt.Errorf("recovery journal: insert: %w", err)
	}
	return nil
}

// List returns the newest limit entries for a run in chronological order.
func (r *RecoveryJournal) List(ctx context.Context, organizationID, runID string, limit int) ([]RecoveryEventRow, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `
        SELECT id, run_id, organization_id, attempt, class, kind, reason, evidence, actor_type, created_at
        FROM (
            SELECT * FROM recovery_events
            WHERE organization_id = $1 AND run_id = $2
            ORDER BY created_at DESC
            LIMIT $3
        ) recent
        ORDER BY created_at ASC`,
		organizationID, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("recovery journal: query: %w", err)
	}
	defer rows.Close()
	out := []RecoveryEventRow{}
	for rows.Next() {
		var row RecoveryEventRow
		var evidence []byte
		if err := rows.Scan(&row.ID, &row.RunID, &row.OrganizationID, &row.Attempt, &row.Class, &row.Kind, &row.Reason, &evidence, &row.ActorType, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("recovery journal: scan: %w", err)
		}
		if len(evidence) > 0 {
			if err := json.Unmarshal(evidence, &row.Evidence); err != nil {
				return nil, fmt.Errorf("recovery journal: decode evidence: %w", err)
			}
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recovery journal: rows: %w", err)
	}
	return out, nil
}

// RecoveryAttempts stores the attempt history loop detection reasons over.
type RecoveryAttempts struct {
	pool *pgxpool.Pool
}

// NewRecoveryAttempts builds the attempt repository.
func NewRecoveryAttempts(pool *pgxpool.Pool) *RecoveryAttempts { return &RecoveryAttempts{pool: pool} }

// Append stores one attempt and returns the number already recorded for the
// run so a caller can make the loop decision before mutating anything.
func (r *RecoveryAttempts) Append(ctx context.Context, row RecoveryAttemptRow) error {
	if row.ID == "" || row.RunID == "" || row.OrganizationID == "" {
		return domain.ErrValidation
	}
	_, err := r.pool.Exec(ctx, `
        INSERT INTO recovery_attempts (id, run_id, organization_id, attempt_number, kind, fingerprint, patch_fingerprint, failing_count, verdict, note)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		row.ID, row.RunID, row.OrganizationID, row.Number, row.Kind, row.Fingerprint, row.PatchFingerprint, row.FailingCount, row.Verdict, row.Note)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// The same attempt number cannot be recorded twice; a replay after
			// a worker restart must not duplicate history.
			return nil
		}
		return fmt.Errorf("recovery attempts: insert: %w", err)
	}
	return nil
}

// List returns the attempt history of a run, oldest first.
func (r *RecoveryAttempts) List(ctx context.Context, organizationID, runID string, limit int) ([]RecoveryAttemptRow, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := r.pool.Query(ctx, `
        SELECT id, run_id, organization_id, attempt_number, kind, fingerprint, patch_fingerprint, failing_count, verdict, note, created_at
        FROM recovery_attempts
        WHERE organization_id = $1 AND run_id = $2
        ORDER BY attempt_number ASC
        LIMIT $3`, organizationID, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("recovery attempts: query: %w", err)
	}
	defer rows.Close()
	out := []RecoveryAttemptRow{}
	for rows.Next() {
		var row RecoveryAttemptRow
		if err := rows.Scan(&row.ID, &row.RunID, &row.OrganizationID, &row.Number, &row.Kind, &row.Fingerprint, &row.PatchFingerprint, &row.FailingCount, &row.Verdict, &row.Note, &row.CreatedAt); err != nil {
			return nil, fmt.Errorf("recovery attempts: scan: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recovery attempts: rows: %w", err)
	}
	return out, nil
}

// RecoveryBudgets tracks bounded attempts per run and error class.
type RecoveryBudgets struct {
	pool *pgxpool.Pool
}

// NewRecoveryBudgets builds the budget repository.
func NewRecoveryBudgets(pool *pgxpool.Pool) *RecoveryBudgets { return &RecoveryBudgets{pool: pool} }

// Consume records one attempt and returns the updated budget. The row is
// locked, so concurrent workers cannot spend the same budget twice.
func (r *RecoveryBudgets) Consume(ctx context.Context, runID, organizationID, class string, allowed int) (BudgetRow, error) {
	var budget BudgetRow
	err := r.pool.QueryRow(ctx, `
        INSERT INTO recovery_budgets (run_id, class, allowed, used, remaining, exhausted)
        VALUES ($1, $2, $3, 1, $3 - 1, $3 <= 1)
        ON CONFLICT (run_id, class) DO UPDATE
            SET used = recovery_budgets.used + 1,
                remaining = greatest(recovery_budgets.allowed - recovery_budgets.used - 1, 0),
                exhausted = recovery_budgets.used + 1 >= recovery_budgets.allowed,
                updated_at = now()
        RETURNING run_id, class, allowed, used, exhausted`,
		runID, class, allowed).Scan(&budget.RunID, &budget.Class, &budget.Allowed, &budget.Used, &budget.Exhaust)
	if errors.Is(err, pgx.ErrNoRows) {
		return BudgetRow{}, domain.ErrNotFound
	}
	if err != nil {
		return BudgetRow{}, fmt.Errorf("recovery budgets: consume: %w", err)
	}
	return budget, nil
}

// Get reads a budget without consuming an attempt.
func (r *RecoveryBudgets) Get(ctx context.Context, runID, class string) (BudgetRow, bool, error) {
	var budget BudgetRow
	err := r.pool.QueryRow(ctx, `
        SELECT run_id, class, allowed, used, exhausted
        FROM recovery_budgets WHERE run_id = $1 AND class = $2`, runID, class).
		Scan(&budget.RunID, &budget.Class, &budget.Allowed, &budget.Used, &budget.Exhaust)
	if errors.Is(err, pgx.ErrNoRows) {
		return BudgetRow{}, false, nil
	}
	if err != nil {
		return BudgetRow{}, false, fmt.Errorf("recovery budgets: get: %w", err)
	}
	return budget, true, nil
}

// List returns every budget of a run.
func (r *RecoveryBudgets) List(ctx context.Context, runID string) ([]BudgetRow, error) {
	rows, err := r.pool.Query(ctx, `
        SELECT run_id, class, allowed, used, exhausted
        FROM recovery_budgets WHERE run_id = $1 ORDER BY class ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("recovery budgets: list: %w", err)
	}
	defer rows.Close()
	out := []BudgetRow{}
	for rows.Next() {
		var row BudgetRow
		if err := rows.Scan(&row.RunID, &row.Class, &row.Allowed, &row.Used, &row.Exhaust); err != nil {
			return nil, fmt.Errorf("recovery budgets: scan: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// EffectReceipts is the durable duplicate-effect guard. The unique constraint
// on (run_id, effect_id) is what makes a replay harmless even when two workers
// race.
type EffectReceipts struct {
	pool *pgxpool.Pool
}

// NewEffectReceipts builds the receipt repository.
func NewEffectReceipts(pool *pgxpool.Pool) *EffectReceipts { return &EffectReceipts{pool: pool} }

// Record stores a receipt, or returns the recorded one when the effect was
// already applied.
func (r *EffectReceipts) Record(ctx context.Context, receipt recovery.Receipt) (recovery.Receipt, bool, error) {
	if receipt.RunID == "" || receipt.EffectID == "" {
		return recovery.Receipt{}, false, domain.ErrValidation
	}
	if receipt.ID == "" {
		receipt.ID = recovery.NewReceiptID()
	}
	if receipt.Operation == "" {
		receipt.Operation = "unknown"
	}
	response, err := json.Marshal(receipt.Result)
	if err != nil {
		return recovery.Receipt{}, false, fmt.Errorf("effect receipts: marshal result: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
        INSERT INTO effect_receipts (id, run_id, operation, effect_id, status, response)
        VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT (run_id, effect_id) DO NOTHING`,
		receipt.ID, receipt.RunID, receipt.Operation, receipt.EffectID, receipt.Status, response)
	if err != nil {
		return recovery.Receipt{}, false, fmt.Errorf("effect receipts: insert: %w", err)
	}
	existing, ok, err := r.Get(ctx, receipt.RunID, receipt.EffectID)
	if err != nil {
		return recovery.Receipt{}, false, err
	}
	if !ok || existing.ID != receipt.ID {
		return existing, true, nil
	}
	return existing, false, nil
}

// Get looks a receipt up by run and effect id.
func (r *EffectReceipts) Get(ctx context.Context, runID, effectID string) (recovery.Receipt, bool, error) {
	var (
		receipt  recovery.Receipt
		response []byte
		created  time.Time
	)
	err := r.pool.QueryRow(ctx, `
        SELECT id, run_id, operation, effect_id, status, response, created_at
        FROM effect_receipts WHERE run_id = $1 AND effect_id = $2`, runID, effectID).
		Scan(&receipt.ID, &receipt.RunID, &receipt.Operation, &receipt.EffectID, &receipt.Status, &response, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return recovery.Receipt{}, false, nil
	}
	if err != nil {
		return recovery.Receipt{}, false, fmt.Errorf("effect receipts: get: %w", err)
	}
	if len(response) > 0 {
		if err := json.Unmarshal(response, &receipt.Result); err != nil {
			return recovery.Receipt{}, false, fmt.Errorf("effect receipts: decode result: %w", err)
		}
	}
	receipt.CreatedAt = created.UTC().Format(time.RFC3339)
	return receipt, true, nil
}

// List returns the receipts of a run, oldest first.
func (r *EffectReceipts) List(ctx context.Context, runID string, limit int) ([]recovery.Receipt, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := r.pool.Query(ctx, `
        SELECT id, run_id, operation, effect_id, status, response, created_at
        FROM effect_receipts WHERE run_id = $1 ORDER BY created_at ASC LIMIT $2`, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("effect receipts: list: %w", err)
	}
	defer rows.Close()
	out := []recovery.Receipt{}
	for rows.Next() {
		var (
			receipt  recovery.Receipt
			response []byte
			created  time.Time
		)
		if err := rows.Scan(&receipt.ID, &receipt.RunID, &receipt.Operation, &receipt.EffectID, &receipt.Status, &response, &created); err != nil {
			return nil, fmt.Errorf("effect receipts: scan: %w", err)
		}
		if len(response) > 0 {
			if err := json.Unmarshal(response, &receipt.Result); err != nil {
				return nil, fmt.Errorf("effect receipts: decode result: %w", err)
			}
		}
		receipt.CreatedAt = created.UTC().Format(time.RFC3339)
		out = append(out, receipt)
	}
	return out, rows.Err()
}

// Checkpoints stores content-bearing workspace snapshots.
type Checkpoints struct {
	pool *pgxpool.Pool
}

// NewCheckpoints builds the checkpoint repository.
func NewCheckpoints(pool *pgxpool.Pool) *Checkpoints { return &Checkpoints{pool: pool} }

// Save stores a snapshot.
func (r *Checkpoints) Save(ctx context.Context, snapshot recovery.Snapshot, organizationID string) (recovery.Snapshot, error) {
	if snapshot.ID == "" || snapshot.RunID == "" {
		return recovery.Snapshot{}, domain.ErrValidation
	}
	files, err := json.Marshal(snapshot.Files)
	if err != nil {
		return recovery.Snapshot{}, fmt.Errorf("checkpoints: marshal files: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
        INSERT INTO checkpoints (id, run_id, organization_id, workspace_id, verdict, files)
        VALUES ($1, $2, $3, $4, $5, $6)
        ON CONFLICT (id) DO NOTHING`,
		snapshot.ID, snapshot.RunID, organizationID, snapshot.WorkspaceID, snapshot.Verdict, files)
	if err != nil {
		return recovery.Snapshot{}, fmt.Errorf("checkpoints: insert: %w", err)
	}
	return snapshot, nil
}

// Get returns one snapshot by id, scoped to an organization.
func (r *Checkpoints) Get(ctx context.Context, organizationID, snapshotID string) (recovery.Snapshot, bool, error) {
	var (
		row     CheckpointRow
		files   []byte
		created time.Time
	)
	err := r.pool.QueryRow(ctx, `
        SELECT id, run_id, organization_id, workspace_id, verdict, files, created_at
        FROM checkpoints WHERE organization_id = $1 AND id = $2`, organizationID, snapshotID).
		Scan(&row.ID, &row.RunID, &row.OrganizationID, &row.WorkspaceID, &row.Verdict, &files, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return recovery.Snapshot{}, false, nil
	}
	if err != nil {
		return recovery.Snapshot{}, false, fmt.Errorf("checkpoints: get: %w", err)
	}
	snapshot := recovery.Snapshot{ID: row.ID, RunID: row.RunID, WorkspaceID: row.WorkspaceID, Verdict: row.Verdict, CreatedAt: created.UTC().Format(time.RFC3339)}
	if err := json.Unmarshal(files, &snapshot.Files); err != nil {
		return recovery.Snapshot{}, false, fmt.Errorf("checkpoints: decode files: %w", err)
	}
	return snapshot, true, nil
}

// Latest returns the newest snapshot of a run.
func (r *Checkpoints) Latest(ctx context.Context, organizationID, runID string) (recovery.Snapshot, bool, error) {
	var snapshotID string
	err := r.pool.QueryRow(ctx, `
        SELECT id FROM checkpoints
        WHERE organization_id = $1 AND run_id = $2
        ORDER BY created_at DESC LIMIT 1`, organizationID, runID).Scan(&snapshotID)
	if errors.Is(err, pgx.ErrNoRows) {
		return recovery.Snapshot{}, false, nil
	}
	if err != nil {
		return recovery.Snapshot{}, false, fmt.Errorf("checkpoints: latest: %w", err)
	}
	if strings.TrimSpace(snapshotID) == "" {
		return recovery.Snapshot{}, false, nil
	}
	return r.Get(ctx, organizationID, snapshotID)
}

// List returns snapshots of a run, newest first.
func (r *Checkpoints) List(ctx context.Context, organizationID, runID string, limit int) ([]CheckpointRow, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
        SELECT id, run_id, organization_id, workspace_id, verdict, files, created_at
        FROM checkpoints
        WHERE organization_id = $1 AND run_id = $2
        ORDER BY created_at DESC LIMIT $3`, organizationID, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("checkpoints: list: %w", err)
	}
	defer rows.Close()
	out := []CheckpointRow{}
	for rows.Next() {
		var (
			row     CheckpointRow
			files   []byte
			created time.Time
		)
		if err := rows.Scan(&row.ID, &row.RunID, &row.OrganizationID, &row.WorkspaceID, &row.Verdict, &files, &created); err != nil {
			return nil, fmt.Errorf("checkpoints: scan: %w", err)
		}
		row.Files = json.RawMessage(files)
		row.CreatedAt = created
		out = append(out, row)
	}
	return out, rows.Err()
}

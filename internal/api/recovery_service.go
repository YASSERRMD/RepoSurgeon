package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/recovery"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
	"strconv"
)

// RecoveryStores is the durable state the recovery endpoints read and write.
// The repositories in internal/store satisfy it through recoveryGateway;
// tests substitute fakes so the decision logic can be exercised without
// PostgreSQL.
type RecoveryStores interface {
	AppendJournal(ctx context.Context, event store.RecoveryEventRow) error
	ListJournal(ctx context.Context, organizationID, runID string, limit int) ([]store.RecoveryEventRow, error)
	ListAttempts(ctx context.Context, organizationID, runID string, limit int) ([]store.RecoveryAttemptRow, error)
	ConsumeBudget(ctx context.Context, runID, organizationID, class string, allowed int) (store.BudgetRow, error)
	ListBudgets(ctx context.Context, runID string) ([]store.BudgetRow, error)
	RecordReceipt(ctx context.Context, receipt recovery.Receipt) (recovery.Receipt, bool, error)
	ListReceipts(ctx context.Context, runID string, limit int) ([]recovery.Receipt, error)
	SaveCheckpoint(ctx context.Context, snapshot recovery.Snapshot, organizationID string) (recovery.Snapshot, error)
	GetCheckpoint(ctx context.Context, organizationID, snapshotID string) (recovery.Snapshot, bool, error)
	LatestCheckpoint(ctx context.Context, organizationID, runID string) (recovery.Snapshot, bool, error)
	ListCheckpoints(ctx context.Context, organizationID, runID string, limit int) ([]store.CheckpointRow, error)
}

// RecoveryGateway adapts the durable repositories to RecoveryStores.
type RecoveryGateway struct {
	Journal     *store.RecoveryJournal
	Attempts    *store.RecoveryAttempts
	Budgets     *store.RecoveryBudgets
	Receipts    *store.EffectReceipts
	Checkpoints *store.Checkpoints
}

// NewRecoveryGateway binds the repositories of one pool.
func NewRecoveryGateway(pool *pgxpool.Pool) *RecoveryGateway {
	return &RecoveryGateway{
		Journal:     store.NewRecoveryJournal(pool),
		Attempts:    store.NewRecoveryAttempts(pool),
		Budgets:     store.NewRecoveryBudgets(pool),
		Receipts:    store.NewEffectReceipts(pool),
		Checkpoints: store.NewCheckpoints(pool),
	}
}

// AppendJournal stores one journal entry.
func (g *RecoveryGateway) AppendJournal(ctx context.Context, event store.RecoveryEventRow) error {
	return g.Journal.Append(ctx, event)
}

// ListJournal lists journal entries of a run.
func (g *RecoveryGateway) ListJournal(ctx context.Context, organizationID, runID string, limit int) ([]store.RecoveryEventRow, error) {
	return g.Journal.List(ctx, organizationID, runID, limit)
}

// ListAttempts lists attempt history of a run.
func (g *RecoveryGateway) ListAttempts(ctx context.Context, organizationID, runID string, limit int) ([]store.RecoveryAttemptRow, error) {
	return g.Attempts.List(ctx, organizationID, runID, limit)
}

// ConsumeBudget records one attempt against a class budget.
func (g *RecoveryGateway) ConsumeBudget(ctx context.Context, runID, organizationID, class string, allowed int) (store.BudgetRow, error) {
	return g.Budgets.Consume(ctx, runID, organizationID, class, allowed)
}

// ListBudgets lists the budgets of a run.
func (g *RecoveryGateway) ListBudgets(ctx context.Context, runID string) ([]store.BudgetRow, error) {
	return g.Budgets.List(ctx, runID)
}

// RecordReceipt records an effect receipt and reports replays.
func (g *RecoveryGateway) RecordReceipt(ctx context.Context, receipt recovery.Receipt) (recovery.Receipt, bool, error) {
	return g.Receipts.Record(ctx, receipt)
}

// ListReceipts lists receipts of a run.
func (g *RecoveryGateway) ListReceipts(ctx context.Context, runID string, limit int) ([]recovery.Receipt, error) {
	return g.Receipts.List(ctx, runID, limit)
}

// SaveCheckpoint stores a snapshot.
func (g *RecoveryGateway) SaveCheckpoint(ctx context.Context, snapshot recovery.Snapshot, organizationID string) (recovery.Snapshot, error) {
	return g.Checkpoints.Save(ctx, snapshot, organizationID)
}

// GetCheckpoint loads one snapshot.
func (g *RecoveryGateway) GetCheckpoint(ctx context.Context, organizationID, snapshotID string) (recovery.Snapshot, bool, error) {
	return g.Checkpoints.Get(ctx, organizationID, snapshotID)
}

// LatestCheckpoint loads the newest snapshot of a run.
func (g *RecoveryGateway) LatestCheckpoint(ctx context.Context, organizationID, runID string) (recovery.Snapshot, bool, error) {
	return g.Checkpoints.Latest(ctx, organizationID, runID)
}

// ListCheckpoints lists snapshots of a run.
func (g *RecoveryGateway) ListCheckpoints(ctx context.Context, organizationID, runID string, limit int) ([]store.CheckpointRow, error) {
	return g.Checkpoints.List(ctx, organizationID, runID, limit)
}

// RecoveryService drives bounded recovery and rollback for a run.
type RecoveryService struct {
	// Stores persists journal, attempts, budgets, receipts and checkpoints.
	Stores RecoveryStores
	// Classifier resolves failure classes and their bounded policies.
	Classifier *recovery.Classifier
	// Detector proves loops from recorded attempts.
	Detector *recovery.Detector
	// WorkspaceDir resolves a workspace id to a directory on disk.
	WorkspaceDir func(workspaceID string) string
}

// NewRecoveryService builds a recovery service over durable stores.
func NewRecoveryService(stores RecoveryStores, workspaceDir func(string) string) *RecoveryService {
	return &RecoveryService{
		Stores:       stores,
		Classifier:   recovery.NewClassifier(),
		Detector:     recovery.NewDetector(),
		WorkspaceDir: workspaceDir,
	}
}

// Block kinds.
const (
	BlockLoopDetected   = "loop_detected"
	BlockRetryExhausted = "retry_exhausted"
)

// errBlocked is the sentinel every refusal to keep retrying wraps.
var errBlocked = errors.New("recovery blocked")

// ErrBlocked reports whether an error refuses an unbounded retry.
func ErrBlocked(err error) bool { return errors.Is(err, errBlocked) }

// BlockedError reports that recovery stopped with evidence instead of
// consuming further attempts.
type BlockedError struct {
	// Kind is loop_detected or retry_exhausted.
	Kind string
	// Class is the error class that stopped the run.
	Class recovery.ErrorClass
	// Reason explains the stop in operator language.
	Reason string
	// Evidence carries the fingerprint, budget and attempt detail.
	Evidence map[string]any
}

// Error renders the blocked reason.
func (e *BlockedError) Error() string { return e.Kind + ": " + e.Reason }

var _ error = (*BlockedError)(nil)

// NewBlockedError builds the error used to refuse an unbounded retry.
func NewBlockedError(kind string, class recovery.ErrorClass, reason string, evidence map[string]any) *BlockedError {
	return &BlockedError{Kind: kind, Class: class, Reason: reason, Evidence: evidence}
}

// blockedStatus maps a blocked refusal onto its HTTP status. A proven loop is a
// conflict with the recorded evidence; a spent budget is throttled.
func blockedStatus(blocked *BlockedError) int {
	if blocked == nil {
		return 400
	}
	switch blocked.Kind {
	case BlockLoopDetected:
		return 409
	case BlockRetryExhausted:
		return 429
	}
	return 400
}

// BlockedErrorFrom extracts a BlockedError from an error chain.
func BlockedErrorFrom(err error) (*BlockedError, bool) {
	var blocked *BlockedError
	if errors.As(err, &blocked) {
		return blocked, true
	}
	return nil, false
}

// RecoverOutcome is the result of a bounded recovery decision.
type RecoverOutcome struct {
	// Attempt is the attempt number recovery planned for.
	Attempt int `json:"attempt"`
	// Budget is the class budget as of this decision.
	Budget store.BudgetRow `json:"budget"`
	// DelayMS is the backoff before the attempt runs.
	DelayMS int `json:"delay_ms"`
	// Hypothesis is the focused plan the attempt follows.
	Hypothesis recovery.Hypothesis `json:"hypothesis"`
}

// Recover decides the next bounded step for a run and records it. A proven
// loop or an exhausted budget stops the run as blocked with evidence instead
// of spending another attempt.
func (s *RecoveryService) Recover(ctx context.Context, organizationID, runID string, class recovery.ErrorClass, reason string) (RecoverOutcome, *BlockedError, error) {
	policy, ok := s.Classifier.Policy(class)
	if !ok {
		policy = recovery.DefaultPolicies()[recovery.ClassUnknown]
	}
	history, err := s.Stores.ListAttempts(ctx, organizationID, runID, 0)
	if err != nil {
		return RecoverOutcome{}, nil, err
	}
	attempts := make([]recovery.Attempt, 0, len(history))
	for _, row := range history {
		attempts = append(attempts, recovery.Attempt{
			RunID:            row.RunID,
			Number:           row.Number,
			Kind:             recovery.AttemptKind(row.Kind),
			Fingerprint:      row.Fingerprint,
			PatchFingerprint: row.PatchFingerprint,
			FailingCount:     row.FailingCount,
			Verdict:          row.Verdict,
			Note:             row.Note,
		})
	}
	attempt := len(history) + 1
	if finding := s.Detector.Find(attempts); finding != nil {
		blocked := NewBlockedError(
			BlockLoopDetected,
			class,
			finding.Evidence,
			map[string]any{
				"loop":        finding,
				"attempt":     finding.Attempts,
				"fingerprint": finding.Fingerprint,
			},
		)
		return RecoverOutcome{Attempt: attempt}, blocked, errBlocked
	}
	budget, err := s.Stores.ConsumeBudget(ctx, runID, organizationID, class.String(), policy.MaxAttempts)
	if err != nil {
		return RecoverOutcome{Attempt: attempt}, nil, err
	}
	if budget.Exhaust {
		blocked := NewBlockedError(
			BlockRetryExhausted,
			class,
			"the bounded retry budget for "+class.String()+" failures is spent after "+itoa(budget.Used)+" attempts",
			map[string]any{"budget": budget, "attempts": len(history)},
		)
		return RecoverOutcome{Attempt: attempt, Budget: budget}, blocked, errBlocked
	}
	delay := time.Duration(0)
	if attempt > 1 {
		delay = recovery.Delay(policy, attempt, 0)
	}
	journalEvent := store.RecoveryEventRow{
		ID:             recovery.NewEventID(),
		RunID:          runID,
		OrganizationID: organizationID,
		Attempt:        attempt,
		Class:          class.String(),
		Kind:           string(recovery.EventRetryScheduled),
		Reason:         reasonOr(reason, "recovery scheduled the next bounded attempt"),
		Evidence: map[string]any{
			"delay_ms":               int(delay / time.Millisecond),
			"budget":                 budget,
			"hypothesis_fingerprint": lastFingerprint(attempts),
		},
	}
	if err := s.Stores.AppendJournal(ctx, journalEvent); err != nil {
		return RecoverOutcome{Attempt: attempt, Budget: budget}, nil, err
	}
	hypothesis := recovery.Hypothesis{
		Class:       class,
		Profiles:    []string{"go.test"},
		Fingerprint: lastFingerprint(attempts),
		Rationale:   "re-run the failing profiles on the current workspace after the operator-approved repair",
	}
	if reason != "" {
		hypothesis.Rationale = reason
	}
	return RecoverOutcome{Attempt: attempt, Budget: budget, DelayMS: int(delay / time.Millisecond), Hypothesis: hypothesis}, nil, nil
}

// Snapshot captures a workspace snapshot and stores it as a known-good state.
func (s *RecoveryService) Snapshot(ctx context.Context, organizationID, runID, workspaceID, root, verdict string) (recovery.Snapshot, error) {
	snapshot, err := recovery.Capture(root, runID, workspaceID, verdict)
	if err != nil {
		return recovery.Snapshot{}, err
	}
	return s.Stores.SaveCheckpoint(ctx, snapshot, organizationID)
}

// RollbackOutcome reports what a rollback actually changed.
type RollbackOutcome struct {
	// CheckpointID is the snapshot that was restored.
	CheckpointID string `json:"checkpoint_id"`
	// RestoredFiles lists the files written back to the snapshot state.
	RestoredFiles []string `json:"restored_files"`
	// DriftBefore lists the files that differed from the snapshot.
	DriftBefore int `json:"drift_before"`
	// DriftAfter lists files still differing after the restore.
	DriftAfter int `json:"drift_after"`
	// Receipt is the idempotent record of the restore.
	Receipt recovery.Receipt `json:"receipt"`
	// Duplicate reports whether the restore was already recorded.
	Duplicate bool `json:"duplicate"`
}

// Rollback restores a workspace to a known-good snapshot. The restore is
// guarded by an effect receipt, so a replayed restore returns the recorded
// outcome instead of writing the workspace twice.
func (s *RecoveryService) Rollback(ctx context.Context, organizationID, runID, workspaceID, effectID, checkpointID, reason string) (RollbackOutcome, error) {
	if effectID == "" {
		return RollbackOutcome{}, errEffectIDRequired
	}
	recorded, duplicate, err := s.Stores.RecordReceipt(ctx, recovery.Receipt{
		ID:        recovery.NewReceiptID(),
		RunID:     runID,
		Operation: "restore_checkpoint",
		EffectID:  effectID,
		Status:    "applied",
	})
	if err != nil {
		return RollbackOutcome{}, err
	}
	if duplicate {
		return RollbackOutcome{CheckpointID: stringValue(recorded.Result, "checkpoint_id"), Receipt: recorded, Duplicate: true}, nil
	}
	snapshot, ok, err := resolveSnapshot(ctx, s.Stores, organizationID, runID, checkpointID)
	if err != nil {
		return RollbackOutcome{}, err
	}
	if !ok {
		return RollbackOutcome{}, errCheckpointNotFound
	}
	root := s.rootFor(workspaceID)
	if root == "" {
		return RollbackOutcome{}, errWorkspaceUnknown
	}
	drift, err := snapshot.Drift(root)
	if err != nil {
		return RollbackOutcome{}, err
	}
	restored, err := snapshot.Restore(root)
	if err != nil {
		return RollbackOutcome{}, err
	}
	after, err := snapshot.Drift(root)
	if err != nil {
		return RollbackOutcome{}, err
	}
	journalEvidence := map[string]any{
		"checkpoint_id":  snapshot.ID,
		"restored_files": len(restored),
		"drift_before":   len(drift),
		"drift_after":    len(after),
		"effect_id":      effectID,
	}
	if err := s.Stores.AppendJournal(ctx, store.RecoveryEventRow{
		ID:             recovery.NewEventID(),
		RunID:          runID,
		OrganizationID: organizationID,
		Kind:           string(recovery.EventRollback),
		Reason:         reasonOr(reason, "restored the workspace to a known-good checkpoint"),
		Evidence:       journalEvidence,
		ActorType:      "user",
	}); err != nil {
		return RollbackOutcome{}, err
	}
	recorded.Result = map[string]any{
		"checkpoint_id":  snapshot.ID,
		"restored_files": len(restored),
		"drift_before":   len(drift),
		"drift_after":    len(after),
		"restored":       restored,
	}
	return RollbackOutcome{
		CheckpointID:  snapshot.ID,
		RestoredFiles: restored,
		DriftBefore:   len(drift),
		DriftAfter:    len(after),
		Receipt:       recorded,
	}, nil
}

func resolveSnapshot(ctx context.Context, stores RecoveryStores, organizationID, runID, checkpointID string) (recovery.Snapshot, bool, error) {
	if checkpointID != "" {
		return stores.GetCheckpoint(ctx, organizationID, checkpointID)
	}
	return stores.LatestCheckpoint(ctx, organizationID, runID)
}

func (s *RecoveryService) rootFor(workspaceID string) string {
	if s.WorkspaceDir == nil {
		return ""
	}
	return s.WorkspaceDir(workspaceID)
}

// RecoveryView is the full recovery state of a run for the operator UI.
type RecoveryView struct {
	RunID       string             `json:"run_id"`
	RunState    string             `json:"run_state"`
	Budgets     []recovery.Budget  `json:"budgets"`
	Attempts    []recovery.Attempt `json:"attempts"`
	Timeline    []recovery.Event   `json:"timeline"`
	Receipts    []recovery.Receipt `json:"receipts"`
	Checkpoints []CheckpointView   `json:"checkpoints"`
	NextAttempt int                `json:"next_attempt"`
}

// CheckpointView describes one known-good snapshot.
type CheckpointView struct {
	ID        string `json:"id"`
	RunID     string `json:"run_id"`
	Verdict   string `json:"verdict"`
	FileCount int    `json:"file_count"`
	CreatedAt string `json:"created_at"`
}

// Report assembles the recovery view of a run from durable state.
func (s *RecoveryService) Report(ctx context.Context, organizationID, runID string) (RecoveryView, error) {
	view := RecoveryView{RunID: runID}
	events, err := s.Stores.ListJournal(ctx, organizationID, runID, 50)
	if err != nil {
		return view, err
	}
	for _, row := range events {
		view.Timeline = append(view.Timeline, toRecoveryEvent(row))
	}
	history, err := s.Stores.ListAttempts(ctx, organizationID, runID, 50)
	if err != nil {
		return view, err
	}
	for _, row := range history {
		view.Attempts = append(view.Attempts, recovery.Attempt{
			RunID:            row.RunID,
			Number:           row.Number,
			Kind:             recovery.AttemptKind(row.Kind),
			Fingerprint:      row.Fingerprint,
			PatchFingerprint: row.PatchFingerprint,
			FailingCount:     row.FailingCount,
			Verdict:          row.Verdict,
			Note:             row.Note,
		})
	}
	view.NextAttempt = len(history) + 1
	budgets, err := s.Stores.ListBudgets(ctx, runID)
	if err != nil {
		return view, err
	}
	for _, row := range budgets {
		view.Budgets = append(view.Budgets, recovery.Budget{
			Class:     recovery.ErrorClass(row.Class),
			Allowed:   row.Allowed,
			Used:      row.Used,
			Remaining: intMax(row.Allowed-row.Used, 0),
			Exhausted: row.Exhaust,
		})
	}
	receipts, err := s.Stores.ListReceipts(ctx, runID, 50)
	if err != nil {
		return view, err
	}
	view.Receipts = receipts
	checkpoints, err := s.Stores.ListCheckpoints(ctx, organizationID, runID, 20)
	if err != nil {
		return view, err
	}
	for _, row := range checkpoints {
		view.Checkpoints = append(view.Checkpoints, CheckpointView{
			ID:        row.ID,
			RunID:     row.RunID,
			Verdict:   row.Verdict,
			FileCount: countJSONFiles(row.Files),
			CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	return view, nil
}

func toRecoveryEvent(row store.RecoveryEventRow) recovery.Event {
	return recovery.Event{
		ID:        row.ID,
		RunID:     row.RunID,
		Kind:      recovery.EventKind(row.Kind),
		Attempt:   row.Attempt,
		Class:     recovery.ErrorClass(row.Class),
		Reason:    row.Reason,
		Evidence:  row.Evidence,
		ActorType: row.ActorType,
		CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
	}
}

func countJSONFiles(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	files := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &files); err != nil {
		return 0
	}
	return len(files)
}

func stringValue(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if value, ok := m[key].(string); ok {
		return value
	}
	return ""
}

func intMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Errors the recovery endpoints surface to operators.
var (
	errEffectIDRequired   = errors.New("recovery: effect_id is required so a restore cannot be replayed")
	errCheckpointNotFound = errors.New("recovery: no known-good checkpoint is available for this run")
	errWorkspaceUnknown   = errors.New("recovery: the workspace could not be resolved")
)

// itoa formats attempt counts for operator-facing reasons.
func itoa(value int) string { return strconv.Itoa(value) }

// reasonOr returns the reason when set, otherwise the fallback.
func reasonOr(reason, fallback string) string {
	if strings.TrimSpace(reason) == "" {
		return fallback
	}
	return reason
}

// lastFingerprint reports the most recent failure signature of a history.
func lastFingerprint(attempts []recovery.Attempt) string {
	if len(attempts) == 0 {
		return ""
	}
	return attempts[len(attempts)-1].Fingerprint
}

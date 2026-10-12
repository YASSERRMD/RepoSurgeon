package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/recovery"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// RecoverRequest is the accepted recover payload.
type RecoverRequest struct {
	// WorkspaceID is the workspace the next attempt runs against.
	WorkspaceID string `json:"workspace_id"`
	// Class overrides the derived failure class, for example rate_limit.
	Class string `json:"class"`
	// Reason explains why recovery was requested.
	Reason string `json:"reason"`
	// CheckpointID restores that snapshot before the attempt runs.
	CheckpointID string `json:"checkpoint_id"`
}

// RollbackRequest is the accepted rollback payload.
type RollbackRequest struct {
	// EffectID makes the restore idempotent: the same id can never restore twice.
	EffectID string `json:"effect_id"`
	// CheckpointID selects a snapshot; empty selects the newest one.
	CheckpointID string `json:"checkpoint_id"`
	// Reason explains why the workspace was rolled back.
	Reason string `json:"reason"`
	// WorkspaceID scopes the workspace that is restored.
	WorkspaceID string `json:"workspace_id"`
}

// handleRecover plans the next bounded repair attempt for a run.
func (s *Server) handleRecover(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "run.execute") {
		return
	}
	if s.recovery == nil {
		writeRecoveryUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	var payload RecoverRequest
	if !decodeInto(w, r, &payload) {
		return
	}
	class := recovery.ClassTestFailure
	if strings.TrimSpace(payload.Class) != "" {
		class = recovery.ErrorClass(strings.TrimSpace(payload.Class))
		if !class.Known() {
			writeDomainError(w, r, &checkError{message: "unknown failure class " + payload.Class})
			return
		}
	}
	outcome, blocked, err := s.recovery.Recover(r.Context(), session.OrganizationID, runID, class, strings.TrimSpace(payload.Reason))
	if err != nil {
		if blocked != nil {
			// The run is parked as blocked with the evidence that proves the
			// loop; the historical timeline is preserved, never rewritten.
			if _, transitionErr := s.runs.Transition(r.Context(), session.OrganizationID, runID, "blocked", blocked.Reason, "user"); transitionErr != nil {
				s.log.Debug("recovery transition to blocked failed", "run_id", runID, "err", transitionErr)
			}
			if journalErr := s.recoveryJournal(r, session, runID, blocked, outcome.Attempt); journalErr != nil {
				s.log.Debug("recovery journal append failed", "run_id", runID, "err", journalErr)
			}
			writeBlockedError(w, r, blocked)
			return
		}
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      run.ProjectID,
		Action:         "run.recover",
		EntityType:     "recovery_attempt",
		EntityID:       runID,
		Payload: map[string]any{
			"attempt":        outcome.Attempt,
			"class":          string(class),
			"delay_ms":       outcome.DelayMS,
			"budget_used":    outcome.Budget.Used,
			"budget_allowed": outcome.Budget.Allowed,
		},
	})
	WriteJSON(w, http.StatusCreated, map[string]any{
		"run_id":     runID,
		"attempt":    outcome.Attempt,
		"budget":     outcome.Budget,
		"delay_ms":   outcome.DelayMS,
		"hypothesis": outcome.Hypothesis,
	})
}

// recoveryJournal appends the blocked event so the timeline shows the stop.
func (s *Server) recoveryJournal(r *http.Request, session sessionSubject, runID string, blocked *BlockedError, attempt int) error {
	if s.recovery == nil {
		return nil
	}
	return s.recovery.Stores.AppendJournal(r.Context(), store.RecoveryEventRow{
		ID:             recovery.NewEventID(),
		RunID:          runID,
		OrganizationID: session.OrganizationID,
		Attempt:        attempt,
		Class:          blocked.Class.String(),
		Kind:           blocked.Kind,
		Reason:         blocked.Reason,
		Evidence:       blocked.Evidence,
		ActorType:      "user",
	})
}

// handleGetRecovery returns the recovery timeline, budgets and checkpoints.
func (s *Server) handleGetRecovery(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "run.execute") {
		return
	}
	if s.recovery == nil {
		writeRecoveryUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	view, err := s.recovery.Report(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	view.RunState = run.State
	WriteJSON(w, http.StatusOK, view)
}

// handleRollback restores a workspace to a known-good checkpoint.
func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "run.execute") {
		return
	}
	if s.recovery == nil {
		writeRecoveryUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	var payload RollbackRequest
	if !decodeInto(w, r, &payload) {
		return
	}
	if strings.TrimSpace(payload.EffectID) == "" {
		writeDomainError(w, r, &checkError{message: "effect_id is required so a restore cannot be replayed"})
		return
	}
	outcome, err := s.recovery.Rollback(
		r.Context(),
		session.OrganizationID,
		runID,
		strings.TrimSpace(payload.WorkspaceID),
		strings.TrimSpace(payload.EffectID),
		strings.TrimSpace(payload.CheckpointID),
		strings.TrimSpace(payload.Reason),
	)
	if err != nil {
		status, code := rollbackErrorMapping(err)
		WriteError(w, r, &Error{Status: status, Code: code, Message: err.Error()}, nil)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      run.ProjectID,
		Action:         "run.rollback",
		EntityType:     "checkpoint",
		EntityID:       outcome.CheckpointID,
		Payload: map[string]any{
			"run_id":       runID,
			"restored":     len(outcome.RestoredFiles),
			"drift_before": outcome.DriftBefore,
			"drift_after":  outcome.DriftAfter,
			"effect_id":    outcome.Receipt.EffectID,
			"duplicate":    outcome.Duplicate,
		},
	})
	status := http.StatusOK
	if outcome.Duplicate {
		// The recorded restore is replayed so a retried request never writes
		// the workspace twice.
		status = http.StatusOK
	}
	WriteJSON(w, status, map[string]any{
		"run_id":         runID,
		"checkpoint_id":  outcome.CheckpointID,
		"restored_files": outcome.RestoredFiles,
		"drift_before":   outcome.DriftBefore,
		"drift_after":    outcome.DriftAfter,
		"receipt":        outcome.Receipt,
		"duplicate":      outcome.Duplicate,
	})
}

// rollbackErrorMapping keeps operator-visible failures honest instead of
// reporting a generic 500.
func rollbackErrorMapping(err error) (int, string) {
	switch {
	case errors.Is(err, errEffectIDRequired):
		return http.StatusBadRequest, "validation_failed"
	case errors.Is(err, errCheckpointNotFound):
		return http.StatusNotFound, "not_found"
	case errors.Is(err, errWorkspaceUnknown):
		return http.StatusServiceUnavailable, "workspace_unavailable"
	default:
		return http.StatusInternalServerError, "internal_error"
	}
}

// writeBlockedError reports a blocked recovery with its evidence.
func writeBlockedError(w http.ResponseWriter, r *http.Request, blocked *BlockedError) {
	WriteError(w, r, &Error{
		Status:  blockedStatus(blocked),
		Code:    "recovery_" + blocked.Kind,
		Message: blocked.Reason,
	}, blocked.Evidence)
}

// writeRecoveryUnavailable marks the recovery service as unconfigured.
func writeRecoveryUnavailable(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, &Error{
		Status:  http.StatusServiceUnavailable,
		Code:    "recovery_unavailable",
		Message: "the recovery service is not configured on this server",
	}, nil)
}

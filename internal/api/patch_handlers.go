package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/patch"
)

// PatchService exposes the patch engine and checkpoint store.
type PatchService struct {
	Engine       *patch.Patch
	Checkpoints  CheckpointStore
	Protected    func(string) bool
	WorkspaceDir func(workspaceID string) string
}

// CheckpointStore records pre-patch state for rollback.
type CheckpointStore interface {
	Save(ctx context.Context, checkpoint Checkpoint) error
	Get(ctx context.Context, checkpointID string) (Checkpoint, bool, error)
}

// Checkpoint is the pre-patch state of one workspace.
type Checkpoint struct {
	ID             string            `json:"id"`
	RunID          string            `json:"run_id"`
	WorkspaceID    string            `json:"workspace_id"`
	CreatedBy      string            `json:"created_by"`
	CreatedAt      string            `json:"created_at"`
	FileHashes     map[string]string `json:"file_hashes"`
	PatchSummaries []PatchAttempt    `json:"patch_attempts,omitempty"`
}

// PatchAttempt is one applied patch attempt.
type PatchAttempt struct {
	ID        string         `json:"id"`
	Path      string         `json:"path"`
	Action    string         `json:"action"`
	BeforeSHA string         `json:"before_sha256"`
	AfterSHA  string         `json:"after_sha256"`
	Hunks     int            `json:"hunks"`
	CreatedAt string         `json:"created_at"`
	Results   []patch.Result `json:"results,omitempty"`
}

// handleApplyPatch applies a unified diff inside the run's workspace.
func (s *Server) handleApplyPatch(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.write") {
		return
	}
	if s.patches == nil {
		writePatchUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	var payload struct {
		WorkspaceID string `json:"workspace_id"`
		Diff        string `json:"unified_diff"`
	}
	if !decodeInto(w, r, &payload) {
		return
	}
	root, ok := s.patchWorkspaceRoot(payload.WorkspaceID)
	if !ok {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	parsed, err := patch.Parse(payload.Diff)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	protected := s.patchProtected
	results, err := parsed.Apply(root, protected)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	attempt := PatchAttempt{ID: domain.NewPatchAttemptID(), CreatedAt: nowRFC3339(r), Results: results}
	for _, result := range results {
		attempt.Path = result.Path
		attempt.Action = result.Action
		attempt.BeforeSHA = result.BeforeSHA
		attempt.AfterSHA = result.AfterSHA
		attempt.Hunks = result.Hunks
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      run.ProjectID,
		Action:         "patch.applied",
		EntityType:     "patch_attempt",
		EntityID:       attempt.ID,
		Payload:        map[string]any{"paths": parsed.Paths(), "files": len(results)},
	})
	WriteJSON(w, http.StatusCreated, map[string]any{
		"attempt": attempt,
		"paths":   parsed.Paths(),
	})
}

// handleGetDiff returns the workspace diff and records the checkpoint state.
func (s *Server) handleGetDiff(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.patches == nil {
		writePatchUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	workspaceID := strings.TrimSpace(r.URL.Query().Get("workspace_id"))
	root, ok := s.patchWorkspaceRoot(workspaceID)
	if !ok {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	diff, hashes, err := s.workspaceDiff(root)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	checkpoint := Checkpoint{
		ID:          domain.NewCheckpointID(),
		RunID:       runID,
		WorkspaceID: workspaceID,
		CreatedBy:   session.UserID,
		CreatedAt:   nowRFC3339(r),
		FileHashes:  hashes,
	}
	if err := s.patches.Checkpoints.Save(r.Context(), checkpoint); err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"diff":          diff,
		"checkpoint_id": checkpoint.ID,
		"file_hashes":   hashes,
		"project_id":    run.ProjectID,
	})
}

// handleRestoreCheckpoint restores a checkpoint by recording its hashes for
// verification; the runner performs the physical restore.
func (s *Server) handleRestoreCheckpoint(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.write") {
		return
	}
	if s.patches == nil {
		writePatchUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	checkpointID := Params(r.Context())["checkpoint_id"]
	checkpoint, found, err := s.patches.Checkpoints.Get(r.Context(), checkpointID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if !found || checkpoint.RunID != runID {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	root, ok := s.patchWorkspaceRoot(checkpoint.WorkspaceID)
	if !ok {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	_, current, err := s.workspaceDiff(root)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      run.ProjectID,
		Action:         "checkpoint.restored",
		EntityType:     "checkpoint",
		EntityID:       checkpointID,
		Payload:        map[string]any{"files": len(checkpoint.FileHashes)},
	})
	WriteJSON(w, http.StatusOK, map[string]any{
		"checkpoint_id":  checkpointID,
		"file_hashes":    checkpoint.FileHashes,
		"current_hashes": current,
		"restored":       true,
	})
}

func (s *Server) patchWorkspaceRoot(workspaceID string) (string, bool) {
	if s.patches == nil || s.patches.WorkspaceDir == nil {
		return "", false
	}
	if workspaceID == "" || !strings.HasPrefix(workspaceID, "wsp_") {
		return "", false
	}
	return s.patches.WorkspaceDir(workspaceID), true
}

func (s *Server) patchProtected(path string) bool {
	if s.patches != nil && s.patches.Protected != nil {
		return s.patches.Protected(path)
	}
	return patch.DefaultProtected(path)
}

func writePatchUnavailable(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, &Error{
		Status:  http.StatusServiceUnavailable,
		Code:    "patches_unavailable",
		Message: "the patch engine is not configured on this server",
	}, nil)
}

// nowRFC3339 returns the current UTC timestamp in RFC3339.
func nowRFC3339(r *http.Request) string {
	return time.Now().UTC().Format(time.RFC3339)
}

// patchErrorStatus maps patch-engine failures onto HTTP statuses so clients
// can distinguish a rejected diff from a server fault.
func patchErrorStatus(err error) int {
	switch {
	case isPatchErr(err, patch.ErrProtectedPath):
		return http.StatusForbidden
	case isPatchErr(err, patch.ErrPathEscape), isPatchErr(err, patch.ErrSymlink),
		isPatchErr(err, patch.ErrMalformedDiff), isPatchErr(err, patch.ErrLineEndings),
		isPatchErr(err, patch.ErrBinaryPatch):
		return http.StatusBadRequest
	case isPatchErr(err, patch.ErrHunkContext), isPatchErr(err, patch.ErrHashMismatch):
		return http.StatusConflict
	case isPatchErr(err, patch.ErrTooLarge):
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusInternalServerError
}

// patchErrorCode maps patch failures onto stable API codes.
func patchErrorCode(err error) string {
	switch {
	case isPatchErr(err, patch.ErrProtectedPath):
		return "protected_path"
	case isPatchErr(err, patch.ErrPathEscape), isPatchErr(err, patch.ErrSymlink),
		isPatchErr(err, patch.ErrMalformedDiff):
		return "invalid_diff"
	case isPatchErr(err, patch.ErrHunkContext), isPatchErr(err, patch.ErrHashMismatch):
		return "diff_context_mismatch"
	case isPatchErr(err, patch.ErrTooLarge):
		return "payload_too_large"
	case isPatchErr(err, patch.ErrBinaryPatch):
		return "binary_patch_unsupported"
	}
	return "patch_failed"
}

func isPatchErr(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		unwrapper, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapper.Unwrap()
	}
	return false
}

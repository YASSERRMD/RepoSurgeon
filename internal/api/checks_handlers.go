package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/checks"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
)

// CheckService bundles the check profiles, runner and store.
type CheckService struct {
	Profiles *checks.ProfileSet
	Runner   checks.Runner
	Store    checks.Store
}

// PostCheckRequest is one requested check run.
type PostCheckRequest struct {
	WorkspaceID string   `json:"workspace_id"`
	Profiles    []string `json:"profiles"`
}

// handleRunChecks executes one or more verified profiles through the runner.
func (s *Server) handleRunChecks(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "run.execute") {
		return
	}
	if s.checks == nil {
		writeCheckUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	var payload PostCheckRequest
	if !decodeInto(w, r, &payload) {
		return
	}
	root, ok := s.checkWorkspaceRoot(payload.WorkspaceID)
	if !ok {
		WriteError(w, r, ErrNotFound, nil)
		return
	}

	requested := payload.Profiles
	if len(requested) == 0 {
		requested = s.checks.Profiles.Required()
	}
	var results []checks.Check
	for _, name := range requested {
		profile, ok := s.checks.Profiles.Get(name)
		if !ok {
			writeDomainError(w, r, &checkError{message: "unknown command profile " + name})
			return
		}
		result, err := s.runOneCheck(r.Context(), runID, profile, root, payload.WorkspaceID)
		if err != nil {
			writeDomainError(w, r, err)
			return
		}
		results = append(results, result)
	}

	// A missing or failed required check blocks the run: green is never
	// assumed.
	verdict := checks.VerdictGreen
	for _, result := range results {
		if result.Verdict != checks.VerdictGreen {
			verdict = checks.VerdictRed
			break
		}
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      run.ProjectID,
		Action:         "checks.executed",
		EntityType:     "run",
		EntityID:       runID,
		Payload:        map[string]any{"profiles": requested, "verdict": verdict},
	})
	WriteJSON(w, http.StatusCreated, map[string]any{
		"run_id":  runID,
		"verdict": verdict,
		"items":   results,
	})
}

// runOneCheck executes a profile and parses its output.
func (s *Server) runOneCheck(ctx context.Context, runID string, profile checks.Profile, root, workspaceID string) (checks.Check, error) {
	check := checks.Check{
		ID:        checks.NewCheckID(),
		RunID:     runID,
		Profile:   profile.Name,
		Required:  profile.Required,
		CreatedAt: time.Now().UTC(),
	}
	result, err := s.checks.Runner.Run(ctx, profile.Name, root, profile.TimeoutMS)
	if err != nil {
		check.Status = "rejected"
		check.Verdict = checks.VerdictBlocked
		check.FailureGroups = []checks.FailureGroup{{Message: err.Error(), Kind: "sandbox"}}
		if saveErr := s.checks.Store.Save(ctx, check); saveErr != nil {
			return check, saveErr
		}
		return check, nil
	}
	check.Status = result.Status
	check.ExitCode = result.ExitCode
	check.DurationMS = result.DurationMS
	check.FailureGroups = checks.ParseFailures(profile.Name, result.Stdout, result.Stderr)
	check.Verdict = checks.VerdictFor(check)
	check.RawLogSHA256 = checks.HashText(result.Stdout + "\n" + result.Stderr)
	if err := s.checks.Store.Save(ctx, check); err != nil {
		return check, err
	}
	return check, nil
}

// handleListChecks lists the checks recorded for a run.
func (s *Server) handleListChecks(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.checks == nil {
		writeCheckUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	items, err := s.checks.Store.List(r.Context(), runID, 100)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"run_id":     runID,
		"project_id": run.ProjectID,
		"items":      items,
	})
}

// handleGetCheckLogs returns the bounded log of one check.
func (s *Server) handleGetCheckLogs(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.checks == nil {
		writeCheckUnavailable(w, r)
		return
	}
	checkID := Params(r.Context())["check_id"]
	check, found, err := s.checks.Store.Get(r.Context(), checkID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if !found {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	WriteJSON(w, http.StatusOK, checkPayload(check))
}

func checkPayload(check checks.Check) map[string]any {
	return map[string]any{
		"id":             check.ID,
		"run_id":         check.RunID,
		"profile":        check.Profile,
		"status":         check.Status,
		"exit_code":      check.ExitCode,
		"duration_ms":    check.DurationMS,
		"required":       check.Required,
		"verdict":        check.Verdict,
		"failure_groups": check.FailureGroups,
		"raw_log_sha256": check.RawLogSHA256,
		"created_at":     check.CreatedAt,
	}
}

func (s *Server) checkWorkspaceRoot(workspaceID string) (string, bool) {
	if workspaceID == "" || !strings.HasPrefix(workspaceID, "wsp_") {
		return "", false
	}
	if s.patches == nil || s.patches.WorkspaceDir == nil {
		return "", false
	}
	return s.patches.WorkspaceDir(workspaceID), true
}

func writeCheckUnavailable(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, &Error{
		Status:  http.StatusServiceUnavailable,
		Code:    "checks_unavailable",
		Message: "the check service is not configured on this server",
	}, nil)
}

// checkError is a validation failure for unknown profiles.
type checkError struct{ message string }

func (e *checkError) Error() string { return e.message }

package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/agent"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/queue"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// handleCreateRun creates a durable run and enqueues its task.
func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "run.execute") {
		return
	}
	if s.runs == nil || s.queue == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "runs_disabled",
			Message: "run scheduling is not configured on this server",
		}, nil)
		return
	}

	var payload struct {
		ProjectID   string         `json:"project_id"`
		IssueID     string         `json:"issue_id"`
		BaseSHA     string         `json:"base_sha"`
		ModelConfig map[string]any `json:"model"`
		Limits      map[string]any `json:"limits"`
		Policy      map[string]any `json:"policy"`
	}
	if !decodeInto(w, r, &payload) {
		return
	}
	project, err := s.services.Projects.GetForOrg(r.Context(), session.OrganizationID, payload.ProjectID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if strings.TrimSpace(payload.BaseSHA) != "" {
		if _, err := s.workspaces.Pin(payload.BaseSHA); err != nil {
			WriteError(w, r, &Error{
				Status:  http.StatusBadRequest,
				Code:    "validation_failed",
				Message: "base_sha must be a 40-character commit sha",
			}, nil)
			return
		}
	}

	runID := domain.NewRunID()
	run, err := s.runs.Create(r.Context(), store.Run{
		ID:             runID,
		OrganizationID: session.OrganizationID,
		ProjectID:      project.ID,
		IssueID:        payload.IssueID,
		State:          agent.StateQueued,
		BaseSHA:        payload.BaseSHA,
		ModelConfig:    payload.ModelConfig,
		Limits:         payload.Limits,
		Policy:         payload.Policy,
		CreatedBy:      session.UserID,
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if _, err := s.queue.Enqueue(r.Context(), queue.Task{
		ID:             domain.NewTaskID(),
		OrganizationID: session.OrganizationID,
		ProjectID:      project.ID,
		RunID:          run.ID,
		Kind:           queue.KindRun,
	}); err != nil {
		// The run exists but is not scheduled: fail it rather than pretend.
		_, _ = s.runs.Transition(r.Context(), session.OrganizationID, run.ID, agent.StateFailed, "task enqueue failed", "user")
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      project.ID,
		Action:         "run.created",
		EntityType:     "run",
		EntityID:       run.ID,
		Payload:        map[string]any{"base_sha": run.BaseSHA},
	})
	WriteJSON(w, http.StatusCreated, runPayload(run))
}

// handleGetRun returns one run with its state history.
func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	history, err := s.runs.History(r.Context(), runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	changes := make([]map[string]any, 0, len(history))
	for _, change := range history {
		changes = append(changes, map[string]any{
			"id": change.ID, "from_state": change.FromState, "to_state": change.ToState,
			"reason": change.Reason, "actor_type": change.ActorType, "created_at": change.CreatedAt,
		})
	}
	out := runPayload(run)
	out["history"] = changes
	WriteJSON(w, http.StatusOK, out)
}

// handleStartRun moves a queued run into the pipeline and enqueues its task.
func (s *Server) handleStartRun(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "run.execute") {
		return
	}
	if s.runs == nil || s.queue == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "runs_disabled",
			Message: "run scheduling is not configured on this server",
		}, nil)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if run.State != agent.StateQueued {
		WriteError(w, r, &Error{
			Status:  http.StatusConflict,
			Code:    "state_conflict",
			Message: "run is not queued; current state is " + run.State,
		}, nil)
		return
	}
	if _, err := s.queue.Enqueue(r.Context(), queue.Task{
		ID:             domain.NewTaskID(),
		OrganizationID: session.OrganizationID,
		ProjectID:      run.ProjectID,
		RunID:          run.ID,
		Kind:           queue.KindRun,
	}); err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      run.ProjectID,
		Action:         "run.started",
		EntityType:     "run",
		EntityID:       run.ID,
	})
	updated, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusAccepted, runPayload(updated))
}

// handleCancelRun cancels a run and its queued task.
func (s *Server) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "run.execute") {
		return
	}
	if s.runs == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "runs_disabled",
			Message: "run scheduling is not configured on this server",
		}, nil)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if _, err := s.runs.Transition(r.Context(), session.OrganizationID, runID, agent.StateCanceled, "canceled by user", "user"); err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      run.ProjectID,
		Action:         "run.canceled",
		EntityType:     "run",
		EntityID:       run.ID,
	})
	WriteJSON(w, http.StatusAccepted, map[string]any{"id": run.ID, "state": agent.StateCanceled})
}

// handleListRuns lists a project's runs.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "project_id query parameter is required",
		}, nil)
		return
	}
	if _, err := s.services.Projects.GetForOrg(r.Context(), session.OrganizationID, projectID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	limit, cursor, paramErr := paginationParams(r)
	if paramErr != nil {
		WriteError(w, r, paramErr, nil)
		return
	}
	page, err := s.runs.ListForProject(r.Context(), projectID, limit, cursor)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, run := range page.Items {
		items = append(items, runPayload(run))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}

func runPayload(run store.Run) map[string]any {
	return map[string]any{
		"id":              run.ID,
		"organization_id": run.OrganizationID,
		"project_id":      run.ProjectID,
		"issue_id":        run.IssueID,
		"state":           run.State,
		"base_sha":        run.BaseSHA,
		"attempt":         run.Attempt,
		"model_config":    run.ModelConfig,
		"limits":          run.Limits,
		"policy":          run.Policy,
		"created_by":      run.CreatedBy,
		"created_at":      run.CreatedAt,
		"updated_at":      run.UpdatedAt,
		"started_at":      timePtr(run.StartedAt),
		"finished_at":     timePtr(run.FinishedAt),
	}
}

func timePtr(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

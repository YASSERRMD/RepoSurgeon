package api

import (
	"net/http"
	"strings"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/gitworkspace"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
)

// handleCreateWorkspace pins a base commit and records a workspace. The actual
// clone is performed by the worker in later milestones; M05 exposes the
// lifecycle surface and the manager that performs it.
func (s *Server) handleCreateWorkspace(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.write") {
		return
	}
	if s.workspaces == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "workspaces_disabled",
			Message: "the workspace manager is not configured on this server",
		}, nil)
		return
	}

	var payload struct {
		ProjectID string `json:"project_id"`
		SourceURL string `json:"source_url"`
		BaseSHA   string `json:"base_sha"`
	}
	if !decodeInto(w, r, &payload) {
		return
	}
	if _, err := s.services.Projects.GetForOrg(r.Context(), session.OrganizationID, payload.ProjectID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	pinned, err := s.workspaces.Pin(payload.BaseSHA)
	if err != nil {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: err.Error(),
		}, nil)
		return
	}
	if strings.TrimSpace(payload.SourceURL) == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "source_url is required",
		}, nil)
		return
	}

	workspaceID := ids.MustNew("wsp")
	workspace, err := s.workspaces.Clone(r.Context(), workspaceID, payload.SourceURL, pinned)
	if err != nil {
		s.audit(r, session.Subject, domain.AuditEvent{
			OrganizationID: session.OrganizationID,
			ProjectID:      payload.ProjectID,
			Action:         "workspace.clone_failed",
			EntityType:     "workspace",
			Payload:        map[string]any{"source_url": payload.SourceURL, "base_sha": pinned},
		})
		WriteError(w, r, &Error{
			Status:  http.StatusBadGateway,
			Code:    "clone_failed",
			Message: "the repository could not be cloned at the pinned commit",
		}, nil)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      payload.ProjectID,
		Action:         "workspace.created",
		EntityType:     "workspace",
		EntityID:       workspace.ID,
		Payload:        map[string]any{"base_sha": workspace.BaseSHA, "default_ref": workspace.DefaultRef},
	})
	WriteJSON(w, http.StatusCreated, map[string]any{
		"id":          workspace.ID,
		"base_sha":    workspace.BaseSHA,
		"source_url":  workspace.SourceURL,
		"default_ref": workspace.DefaultRef,
		"created_at":  workspace.CreatedAt,
	})
}

// handleDestroyWorkspace removes a workspace and its worktrees.
func (s *Server) handleDestroyWorkspace(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.write") {
		return
	}
	if s.workspaces == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "workspaces_disabled",
			Message: "the workspace manager is not configured on this server",
		}, nil)
		return
	}
	workspaceID := Params(r.Context())["workspace_id"]
	if err := s.workspaces.Destroy(r.Context(), workspaceID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		Action:         "workspace.destroyed",
		EntityType:     "workspace",
		EntityID:       workspaceID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleWorkspaceTree lists the files in a workspace checkout.
func (s *Server) handleWorkspaceTree(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.workspaces == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "workspaces_disabled",
			Message: "the workspace manager is not configured on this server",
		}, nil)
		return
	}
	workspaceID := Params(r.Context())["workspace_id"]
	path := s.workspaces.WorkspacePath(workspaceID)
	if !gitworkspace.Exists(path) {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	entries, err := gitworkspace.ListFiles(path)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		items = append(items, map[string]any{
			"path":   entry.Path,
			"is_dir": entry.IsDir,
			"size":   entry.Size,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

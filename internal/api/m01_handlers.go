package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
)

// ---- organizations ---------------------------------------------------------

func (s *Server) handleListOrganizations(w http.ResponseWriter, r *http.Request) {
	subject, ok := requireSubject(w, r)
	if !ok {
		return
	}
	limit, cursor, paramErr := paginationParams(r)
	if paramErr != nil {
		WriteError(w, r, paramErr, nil)
		return
	}
	page, err := s.services.Organizations.ListForUser(r.Context(), subject.UserID, limit, cursor)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items":       page.Items,
		"next_cursor": page.NextCursor,
	})
}

func (s *Server) handleCreateOrganization(w http.ResponseWriter, r *http.Request) {
	s.idempotent(w, r, "POST /api/v1/organizations", func(subject Subject) (int, any) {
		var payload organizationCreateRequest
		if !decodeInto(w, r, &payload) {
			return http.StatusBadRequest, nil
		}
		fields := map[string]string{}
		name := strings.TrimSpace(payload.Name)
		slug := strings.TrimSpace(payload.Slug)
		if name == "" {
			fields["name"] = "name is required"
		} else if len(name) > maxNameLength {
			fields["name"] = "name must be at most 120 characters"
		}
		if slug == "" {
			fields["slug"] = "slug is required"
		} else if !slugPattern.MatchString(slug) {
			fields["slug"] = "slug must be lowercase letters, digits and dashes (3-64 characters)"
		}
		if len(fields) > 0 {
			WriteError(w, r, &Error{
				Status:  http.StatusBadRequest,
				Code:    "validation_failed",
				Message: "request validation failed",
				Details: map[string]any{"fields": fields},
			}, nil)
			return http.StatusBadRequest, nil
		}

		taken, err := s.services.Organizations.SlugTaken(r.Context(), slug)
		if err != nil {
			writeDomainError(w, r, err)
			return http.StatusInternalServerError, nil
		}
		if taken {
			writeDomainError(w, r, domain.NewDuplicateName("organization", slug))
			return http.StatusConflict, nil
		}

		org := domain.Organization{
			ID:        newOrganizationID(),
			Slug:      slug,
			Name:      name,
			CreatedAt: time.Now().UTC(),
		}
		org.UpdatedAt = org.CreatedAt
		if err := s.services.Organizations.Create(r.Context(), org, subject.UserID, domain.RoleOwner); err != nil {
			writeDomainError(w, r, err)
			return http.StatusInternalServerError, nil
		}
		s.audit(r, subject, domain.AuditEvent{
			OrganizationID: org.ID,
			Action:         "organization.created",
			EntityType:     "organization",
			EntityID:       org.ID,
			Payload:        map[string]any{"name": org.Name, "slug": org.Slug},
		})
		stored, err := s.services.Organizations.Get(r.Context(), org.ID)
		if err != nil {
			writeDomainError(w, r, err)
			return http.StatusInternalServerError, nil
		}
		return http.StatusCreated, map[string]any{
			"id":         stored.ID,
			"slug":       stored.Slug,
			"name":       stored.Name,
			"role":       domain.RoleOwner,
			"created_at": stored.CreatedAt,
			"updated_at": stored.UpdatedAt,
		}
	})
}

// ---- projects --------------------------------------------------------------

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	subject, ok := requireSubject(w, r)
	if !ok {
		return
	}
	organizationID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if organizationID == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "organization_id query parameter is required",
		}, nil)
		return
	}
	if _, err := s.services.Organizations.Membership(r.Context(), organizationID, subject.UserID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	limit, cursor, paramErr := paginationParams(r)
	if paramErr != nil {
		WriteError(w, r, paramErr, nil)
		return
	}
	filter := strings.TrimSpace(r.URL.Query().Get("q"))
	page, err := s.services.Projects.ListForOrg(r.Context(), organizationID, filter, limit, cursor)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items":       page.Items,
		"next_cursor": page.NextCursor,
	})
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	s.idempotent(w, r, "POST /api/v1/projects", func(subject Subject) (int, any) {
		var payload projectCreateRequest
		if !decodeInto(w, r, &payload) {
			return http.StatusBadRequest, nil
		}
		fields := map[string]string{}
		organizationID := strings.TrimSpace(payload.OrganizationID)
		name := strings.TrimSpace(payload.Name)
		description := strings.TrimSpace(payload.Description)
		if organizationID == "" {
			fields["organization_id"] = "organization_id is required"
		}
		if name == "" {
			fields["name"] = "name is required"
		} else if len(name) > maxNameLength {
			fields["name"] = "name must be at most 120 characters"
		}
		if len(description) > maxDescriptionLength {
			fields["description"] = "description must be at most 2000 characters"
		}
		if len(fields) > 0 {
			WriteError(w, r, &Error{
				Status:  http.StatusBadRequest,
				Code:    "validation_failed",
				Message: "request validation failed",
				Details: map[string]any{"fields": fields},
			}, nil)
			return http.StatusBadRequest, nil
		}

		project := domain.Project{
			ID:             newProjectID(),
			OrganizationID: organizationID,
			Name:           name,
			Description:    description,
			Settings:       map[string]any{},
			CreatedAt:      time.Now().UTC(),
		}
		project.UpdatedAt = project.CreatedAt
		if err := s.services.Projects.Create(r.Context(), project, subject.UserID); err != nil {
			writeDomainError(w, r, err)
			if domain.IsConflict(err) {
				return http.StatusConflict, nil
			}
			return http.StatusInternalServerError, nil
		}
		s.audit(r, subject, domain.AuditEvent{
			OrganizationID: project.OrganizationID,
			ProjectID:      project.ID,
			Action:         "project.created",
			EntityType:     "project",
			EntityID:       project.ID,
			Payload:        map[string]any{"name": project.Name, "description": project.Description},
		})
		return http.StatusCreated, projectView(project)
	})
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	subject, ok := requireSubject(w, r)
	if !ok {
		return
	}
	projectID := Params(r.Context())["project_id"]
	if len(projectID) == 0 || len(projectID) > maxIDLength {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	project, err := s.services.Projects.GetForOrg(r.Context(), subject.OrganizationID, projectID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, projectView(project))
}

func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request) {
	s.idempotent(w, r, "PATCH /api/v1/projects/{project_id}", func(subject Subject) (int, any) {
		projectID := Params(r.Context())["project_id"]
		if len(projectID) == 0 || len(projectID) > maxIDLength {
			WriteError(w, r, ErrNotFound, nil)
			return http.StatusNotFound, nil
		}
		var payload projectUpdateRequest
		if !decodeInto(w, r, &payload) {
			return http.StatusBadRequest, nil
		}
		update, apiErr := s.validateProjectUpdate(payload)
		if apiErr != nil {
			WriteError(w, r, apiErr, nil)
			return apiErr.Status, nil
		}
		project, err := s.services.Projects.UpdateForOrg(r.Context(), subject.OrganizationID, projectID, payload.Version, update, subject.UserID)
		if err != nil {
			writeDomainError(w, r, err)
			if domain.IsConflict(err) {
				return http.StatusConflict, nil
			}
			if domain.IsNotFound(err) {
				return http.StatusNotFound, nil
			}
			if domain.IsPermissionDenied(err) {
				return http.StatusForbidden, nil
			}
			return http.StatusInternalServerError, nil
		}
		s.audit(r, subject, domain.AuditEvent{
			OrganizationID: project.OrganizationID,
			ProjectID:      project.ID,
			Action:         "project.updated",
			EntityType:     "project",
			EntityID:       project.ID,
			Payload:        map[string]any{"version": project.Version},
		})
		return http.StatusOK, projectView(project)
	})
}

// validateProjectUpdate enforces the contract's field rules, including the
// settings key allowlist.
func (s *Server) validateProjectUpdate(payload projectUpdateRequest) (domain.ProjectUpdate, *Error) {
	fields := map[string]string{}
	update := domain.ProjectUpdate{}
	if payload.Version < 0 {
		fields["version"] = "version must be zero or positive"
	}
	if payload.Name != nil {
		name := strings.TrimSpace(*payload.Name)
		if name == "" {
			fields["name"] = "name must not be empty"
		} else if len(name) > maxNameLength {
			fields["name"] = "name must be at most 120 characters"
		} else {
			update.Name = &name
		}
	}
	if payload.Description != nil {
		description := strings.TrimSpace(*payload.Description)
		if len(description) > maxDescriptionLength {
			fields["description"] = "description must be at most 2000 characters"
		} else {
			update.Description = &description
		}
	}
	if payload.Settings != nil {
		for key, value := range payload.Settings {
			expectedType, known := domain.KnownSettings[key]
			if !known {
				fields["settings"] = "unknown settings key " + key
				continue
			}
			if !matchesSettingsType(value, expectedType) {
				fields["settings"] = "settings key " + key + " must be a " + expectedType
			}
		}
		if len(fields["settings"]) != 0 && !hasKey(fields, "settings") {
			fields["settings"] = "settings object is invalid"
		}
		if _, bad := fields["settings"]; !bad {
			update.Settings = payload.Settings
		}
	}
	if len(fields) > 0 {
		return update, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "request validation failed",
			Details: map[string]any{"fields": fields},
		}
	}
	return update, nil
}

func hasKey(m map[string]string, key string) bool {
	_, ok := m[key]
	return ok
}

// matchesSettingsType checks a settings value against its declared type.
func matchesSettingsType(value any, expected string) bool {
	switch expected {
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	default:
		return false
	}
}

// projectView is the API projection of a project. It never exposes internal
// columns.
func projectView(project domain.Project) map[string]any {
	return map[string]any{
		"id":              project.ID,
		"organization_id": project.OrganizationID,
		"name":            project.Name,
		"description":     project.Description,
		"settings":        project.Settings,
		"version":         project.Version,
		"created_at":      project.CreatedAt,
		"updated_at":      project.UpdatedAt,
	}
}

// ---- audit -----------------------------------------------------------------

func (s *Server) handleListAuditEvents(w http.ResponseWriter, r *http.Request) {
	subject, ok := requireSubject(w, r)
	if !ok {
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
	project, err := s.services.Projects.GetForOrg(r.Context(), subject.OrganizationID, projectID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	limit, cursor, paramErr := paginationParams(r)
	if paramErr != nil {
		WriteError(w, r, paramErr, nil)
		return
	}
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	page, err := s.services.Audit.ListForProject(r.Context(), project.OrganizationID, project.ID, action, limit, cursor)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items":       page.Items,
		"next_cursor": page.NextCursor,
	})
}

// audit writes one audit event for a mutation. Failures are logged, never
// returned to the client.
func (s *Server) audit(r *http.Request, subject Subject, event domain.AuditEvent) {
	recorder := auditRecorder{store: s.services.Audit, log: s.log}
	recorder.record(r.Context(), subject, event)
}

var _ = json.Marshal

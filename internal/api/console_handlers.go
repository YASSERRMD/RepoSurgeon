package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// ConsoleStores is the durable state the console reads: the aggregate metrics
// and the operator's view settings. The repositories in internal/store satisfy
// it; tests substitute fakes so the handlers can be exercised without
// PostgreSQL.
type ConsoleStores interface {
	Dashboard(ctx context.Context, organizationID string) (store.DashboardRow, error)
	GetPreferences(ctx context.Context, userID string) (store.UserPreferences, bool, error)
	SavePreferences(ctx context.Context, userID, organizationID string, view map[string]any) (store.UserPreferences, error)
}

// DashboardBundle adapts the durable repositories to ConsoleStores.
type DashboardBundle struct {
	Metrics     *store.Dashboard
	Preferences *store.Preferences
}

// Dashboard returns the aggregate for one organization.
func (b *DashboardBundle) Dashboard(ctx context.Context, organizationID string) (store.DashboardRow, error) {
	return b.Metrics.Aggregate(ctx, organizationID)
}

// GetPreferences reads the stored view settings.
func (b *DashboardBundle) GetPreferences(ctx context.Context, userID string) (store.UserPreferences, bool, error) {
	return b.Preferences.Get(ctx, userID)
}

// SavePreferences stores the view settings.
func (b *DashboardBundle) SavePreferences(ctx context.Context, userID, organizationID string, view map[string]any) (store.UserPreferences, error) {
	return b.Preferences.Save(ctx, userID, organizationID, view)
}

var _ ConsoleStores = (*DashboardBundle)(nil)

// PreferencesPayload is the accepted settings document.
type PreferencesPayload struct {
	// View is the opaque settings document, validated only by size.
	View map[string]any `json:"view"`
}

// handleGetDashboard returns the real aggregate metrics of the active
// organization. Every count is derived from the run, issue and audit tables, so
// the console never renders a number the server did not compute.
func (s *Server) handleGetDashboard(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.console == nil {
		writeConsoleUnavailable(w, r)
		return
	}
	row, err := s.console.Dashboard(r.Context(), session.OrganizationID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	recent := make([]map[string]any, 0, len(row.RecentRuns))
	for _, run := range row.RecentRuns {
		recent = append(recent, map[string]any{
			"id":           run.ID,
			"project_id":   run.ProjectID,
			"project_name": run.ProjectName,
			"state":        run.State,
			"attempt":      run.Attempt,
			"created_at":   run.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"organization_id":     session.OrganizationID,
		"runs_by_state":       row.RunsByState,
		"open_issues":         row.OpenIssues,
		"blocked_runs":        row.BlockedRuns,
		"failed_runs":         row.FailedRuns,
		"recent_audit_events": row.RecentAuditEvents,
		"recent_runs":         recent,
		"generated_at":        row.GeneratedAt.UTC().Format(time.RFC3339),
	})
}

// handleGetPreferences returns the stored view settings of the caller.
func (s *Server) handleGetPreferences(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if s.console == nil {
		writeConsoleUnavailable(w, r)
		return
	}
	stored, found, err := s.console.GetPreferences(r.Context(), session.UserID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	view := map[string]any{}
	if found {
		view = stored.View
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"user_id": session.UserID,
		"view":    view,
		"stored":  found,
	})
}

// handleSavePreferences stores the view settings of the caller.
func (s *Server) handleSavePreferences(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if s.console == nil {
		writeConsoleUnavailable(w, r)
		return
	}
	var payload PreferencesPayload
	if !decodeInto(w, r, &payload) {
		return
	}
	encoded, sizeErr := jsonSize(payload.View)
	if sizeErr != nil {
		writeDomainError(w, r, &checkError{message: sizeErr.Error()})
		return
	}
	if encoded > maxPreferenceBytes {
		writeDomainError(w, r, &checkError{message: "the view settings document is too large to store"})
		return
	}
	saved, err := s.console.SavePreferences(r.Context(), session.UserID, session.OrganizationID, payload.View)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		Action:         "preferences.saved",
		EntityType:     "user_preferences",
		EntityID:       session.UserID,
		Payload:        map[string]any{"bytes": encoded},
	})
	WriteJSON(w, http.StatusOK, map[string]any{
		"user_id":    saved.UserID,
		"view":       saved.View,
		"updated_at": saved.UpdatedAt.UTC().Format(time.RFC3339),
	})
}

// maxPreferenceBytes bounds the settings document so a client cannot fill the
// preferences column with unrelated data.
const maxPreferenceBytes = 64 * 1024

// jsonSize reports the encoded size of a settings document.
func jsonSize(view map[string]any) (int, error) {
	if view == nil {
		return 2, nil
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		return 0, errSettingsNotEncodable
	}
	return len(encoded), nil
}

// errSettingsNotEncodable marks a settings document that cannot be encoded,
// which the caller reports as a validation failure rather than a 500.
var errSettingsNotEncodable = errors.New("the view settings document could not be encoded")

// writeConsoleUnavailable marks the console service as unconfigured.
func writeConsoleUnavailable(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, &Error{
		Status:  http.StatusServiceUnavailable,
		Code:    "console_unavailable",
		Message: "the console service is not configured on this server",
	}, nil)
}

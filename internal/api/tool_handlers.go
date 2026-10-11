package api

import (
	"net/http"
	"strings"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
)

// handleListTools returns the tool catalog with the caller's effective
// permissions, so the UI can show what the agent may do.
func (s *Server) handleListTools(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.tools == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "tools_unavailable",
			Message: "the tool catalog is not configured on this server",
		}, nil)
		return
	}
	granted := s.permissionResolver(r.Context(), session)
	items := make([]map[string]any, 0)
	for _, def := range s.tools.Catalog().All() {
		items = append(items, map[string]any{
			"name":        def.Name,
			"description": def.Description,
			"risk":        def.Risk,
			"permission":  def.Permission,
			"approval":    def.Approval,
			"sandboxed":   def.Sandboxed,
			"permitted":   s.tools.Permits(def.Name, granted),
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleCreateApprovalRequest records an approval request for a high-risk
// action. It never executes the action.
func (s *Server) handleCreateApprovalRequest(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "run.execute") {
		return
	}
	if s.tools == nil || s.tools.Approvals() == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "approvals_unavailable",
			Message: "the approval gateway is not configured on this server",
		}, nil)
		return
	}
	var payload struct {
		RunID  string `json:"run_id"`
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if !decodeInto(w, r, &payload) {
		return
	}
	action := strings.TrimSpace(payload.Action)
	if action == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "action is required",
		}, nil)
		return
	}
	if _, err := s.tools.Catalog().Get(action); err != nil {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: err.Error(),
		}, nil)
		return
	}
	approval, err := s.tools.Request(r.Context(), ApprovalRequest{
		OrganizationID: session.OrganizationID,
		RunID:          payload.RunID,
		Action:         action,
		Reason:         strings.TrimSpace(payload.Reason),
		RequestedBy:    session.UserID,
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      approval.ProjectID,
		Action:         "approval.requested",
		EntityType:     "approval",
		EntityID:       approval.ID,
		Payload:        map[string]any{"action": action},
	})
	WriteJSON(w, http.StatusCreated, approvalPayload(approval))
}

// handleListApprovals lists the organization's approval requests.
func (s *Server) handleListApprovals(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.tools == nil || s.tools.Approvals() == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "approvals_unavailable",
			Message: "the approval gateway is not configured on this server",
		}, nil)
		return
	}
	page, err := s.tools.Approvals().List(r.Context(), session.OrganizationID, 50, "")
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, approval := range page.Items {
		items = append(items, approvalPayload(approval))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}

// handleDecideApproval approves or rejects a pending request. A requester can
// never approve their own request unless the policy allows it.
func (s *Server) handleDecideApproval(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "approval.decide") {
		return
	}
	if s.tools == nil || s.tools.Approvals() == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "approvals_unavailable",
			Message: "the approval gateway is not configured on this server",
		}, nil)
		return
	}
	approvalID := Params(r.Context())["approval_id"]
	var payload struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if !decodeInto(w, r, &payload) {
		return
	}
	decision := strings.ToLower(strings.TrimSpace(payload.Decision))
	if decision != "approve" && decision != "reject" {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "decision must be approve or reject",
		}, nil)
		return
	}
	approval, err := s.tools.Decide(r.Context(), ApprovalDecision{
		OrganizationID: session.OrganizationID,
		ApprovalID:     approvalID,
		DecidedBy:      session.UserID,
		Approve:        decision == "approve",
		Reason:         strings.TrimSpace(payload.Reason),
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      approval.ProjectID,
		Action:         "approval." + decision + "d",
		EntityType:     "approval",
		EntityID:       approval.ID,
		Payload:        map[string]any{"action": approval.Action},
	})
	WriteJSON(w, http.StatusOK, approvalPayload(approval))
}

func approvalPayload(approval Approval) map[string]any {
	return map[string]any{
		"id":              approval.ID,
		"organization_id": approval.OrganizationID,
		"project_id":      approval.ProjectID,
		"run_id":          approval.RunID,
		"action":          approval.Action,
		"reason":          approval.Reason,
		"state":           approval.State,
		"requested_by":    approval.RequestedBy,
		"decided_by":      approval.DecidedBy,
		"decision_reason": approval.DecisionReason,
		"expires_at":      approval.ExpiresAt,
		"created_at":      approval.CreatedAt,
		"updated_at":      approval.UpdatedAt,
	}
}

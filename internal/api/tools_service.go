package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/tools"
)

// ToolService bundles the M07 catalog, policy and approval gateway.
type ToolService struct {
	catalog   *tools.Catalog
	policy    *tools.Policy
	approvals ApprovalStore
}

// NewToolService builds the tool service.
func NewToolService(approvals ApprovalStore) *ToolService {
	catalog := tools.NewCatalog()
	return &ToolService{catalog: catalog, policy: tools.NewPolicy(catalog), approvals: approvals}
}

// Catalog exposes the tool registry.
func (s *ToolService) Catalog() *tools.Catalog { return s.catalog }

// Policy exposes the authorizer.
func (s *ToolService) Policy() *tools.Policy { return s.policy }

// Permits reports whether a tool is allowed for the granted permissions.
func (s *ToolService) Permits(name string, granted []string) bool {
	return s.policy.Authorize(name, granted).Allowed
}

// Approvals exposes the approval gateway.
func (s *ToolService) Approvals() ApprovalStore { return s.approvals }

// Errors returned by the approval store.
var (
	errApprovalNotFound   = errors.New("approval not found")
	errApprovalNotPending = errors.New("approval is not pending")
	errApprovalExpired    = errors.New("approval has expired")
)

// approvalErrorCode maps the approval sentinels onto stable API codes.
func approvalErrorCode(err error) string {
	switch {
	case errors.Is(err, errApprovalNotFound):
		return "not_found"
	case errors.Is(err, errApprovalNotPending):
		return "approval_not_pending"
	case errors.Is(err, errApprovalExpired):
		return "approval_expired"
	}
	return "internal_error"
}

// Approval errors mapped onto HTTP statuses.
func approvalErrorStatus(err error) int {
	switch {
	case errors.Is(err, errApprovalNotFound):
		return http.StatusNotFound
	case errors.Is(err, errApprovalNotPending), errors.Is(err, errApprovalExpired):
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// Request records a new approval. It is the API-side adapter over the store.
func (s *ToolService) Request(ctx context.Context, req ApprovalRequest) (Approval, error) {
	if s.approvals == nil {
		return Approval{}, errors.New("approval gateway is not configured")
	}
	approval := Approval{
		ID:             domain.NewApprovalID(),
		OrganizationID: req.OrganizationID,
		ProjectID:      req.ProjectID,
		RunID:          req.RunID,
		Action:         req.Action,
		Reason:         req.Reason,
		RequestedBy:    req.RequestedBy,
		ExpiresAt:      nil,
	}
	return s.approvals.Create(ctx, approval)
}

// Decide applies an approval decision.
func (s *ToolService) Decide(ctx context.Context, decision ApprovalDecision) (Approval, error) {
	if s.approvals == nil {
		return Approval{}, errors.New("approval gateway is not configured")
	}
	return s.approvals.Decide(ctx, decision.ApprovalID, decision.Approve, decision.DecidedBy, decision.Reason)
}

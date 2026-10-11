package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/index"
	"github.com/YASSERRMD/RepoSurgeon/internal/plan"
)

// PlanService bundles the M09 plan store and verifier.
type PlanService struct {
	Store  PlanStore
	Verify func(p plan.Plan, exists func(string) bool) []plan.Finding
}

// PlanStore is the persistence surface for plans.
type PlanStore interface {
	SaveDraft(ctx context.Context, draft PlanDraft) (PlanView, error)
	Latest(ctx context.Context, runID string) (PlanView, bool, error)
	Approve(ctx context.Context, runID string, revision int, actorID, reason string) (PlanView, error)
	Reject(ctx context.Context, runID string, revision int, actorID, reason string) (PlanView, error)
}

// PlanDraft is a submitted plan payload.
type PlanDraft struct {
	RunID          string
	OrganizationID string
	ProjectID      string
	BaseSHA        string
	ContextSHA     string
	Plan           plan.Plan
}

// PlanView is the API projection of a plan.
type PlanView struct {
	ID         string                     `json:"id"`
	RunID      string                     `json:"run_id"`
	Revision   int                        `json:"revision"`
	Kind       string                     `json:"kind"`
	Risk       string                     `json:"risk"`
	Summary    string                     `json:"summary"`
	Status     string                     `json:"status"`
	Nodes      []plan.Node                `json:"nodes"`
	Acceptance []plan.AcceptanceCriterion `json:"acceptance"`
	Findings   []plan.Finding             `json:"findings"`
	Approvals  []PlanApproval             `json:"approvals"`
	CreatedAt  string                     `json:"created_at"`
}

// PlanApproval is a recorded decision.
type PlanApproval struct {
	Revision  int    `json:"revision"`
	ActorID   string `json:"actor_id"`
	Decision  string `json:"decision"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

// NewPlanService builds the plan service.
func NewPlanService(store PlanStore, ix *index.Index) *PlanService {
	return &PlanService{
		Store: store,
		Verify: func(p plan.Plan, exists func(string) bool) []plan.Finding {
			return plan.Verify(p, exists, p.Acceptance)
		},
	}
}

// handleSavePlan validates and stores a plan revision for a run.
func (s *Server) handleSavePlan(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "run.execute") {
		return
	}
	if s.plans == nil {
		writePlanUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	if _, err := s.runs.Get(r.Context(), session.OrganizationID, runID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	var payload struct {
		BaseSHA    string    `json:"base_sha"`
		ContextSHA string    `json:"context_sha256"`
		Plan       plan.Plan `json:"plan"`
	}
	if !decodeInto(w, r, &payload) {
		return
	}
	if err := plan.Validate(payload.Plan); err != nil {
		writeDomainError(w, r, err)
		return
	}
	findings := s.plans.Verify(payload.Plan, s.pathExists)
	if plan.HasBlockingFindings(findings) {
		writeDomainError(w, r, &planError{message: "plan verification failed; see findings"})
		return
	}
	view, err := s.plans.Store.SaveDraft(r.Context(), PlanDraft{
		RunID:          runID,
		OrganizationID: session.OrganizationID,
		ProjectID:      session.OrganizationID,
		BaseSHA:        payload.BaseSHA,
		ContextSHA:     payload.ContextSHA,
		Plan:           payload.Plan,
	})
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	view.Findings = findings
	status := plan.StatusDraft
	if plan.RequiresApproval(payload.Plan) {
		status = plan.StatusAwaitingApproval
	}
	view.Status = status
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		Action:         "plan.saved",
		EntityType:     "plan",
		EntityID:       view.ID,
		Payload:        map[string]any{"revision": view.Revision, "risk": payload.Plan.Risk, "status": status},
	})
	WriteJSON(w, http.StatusCreated, view)
}

// handleGetPlan returns the latest plan revision for a run.
func (s *Server) handleGetPlan(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.plans == nil {
		writePlanUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	if _, err := s.runs.Get(r.Context(), session.OrganizationID, runID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	view, found, err := s.plans.Store.Latest(r.Context(), runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if !found {
		WriteError(w, r, &Error{
			Status:  http.StatusNotFound,
			Code:    "plan_not_found",
			Message: "no plan has been created for this run yet",
		}, nil)
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// handleApprovePlan records an approval or rejection.
func (s *Server) handleApprovePlan(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "approval.decide") {
		return
	}
	if s.plans == nil {
		writePlanUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	if _, err := s.runs.Get(r.Context(), session.OrganizationID, runID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	var payload struct {
		Revision int    `json:"revision"`
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
	if payload.Revision <= 0 {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "revision must be a positive integer",
		}, nil)
		return
	}

	var view PlanView
	var err error
	if decision == "approve" {
		view, err = s.plans.Store.Approve(r.Context(), runID, payload.Revision, session.UserID, strings.TrimSpace(payload.Reason))
	} else {
		view, err = s.plans.Store.Reject(r.Context(), runID, payload.Revision, session.UserID, strings.TrimSpace(payload.Reason))
	}
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		Action:         "plan." + decision + "d",
		EntityType:     "plan",
		Payload:        map[string]any{"revision": payload.Revision, "run": runID},
	})
	WriteJSON(w, http.StatusOK, view)
}

// pathExists reports whether a repository path is present in the index.
func (s *Server) pathExists(path string) bool {
	if s.context == nil || s.context.Index == nil || !s.context.Index.Available() {
		return false
	}
	return s.context.Index.PathExists(path)
}

func writePlanUnavailable(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, &Error{
		Status:  http.StatusServiceUnavailable,
		Code:    "plans_unavailable",
		Message: "the plan service is not configured on this server",
	}, nil)
}

// planError is a validation failure surfaced to the client.
type planError struct{ message string }

func (e *planError) Error() string { return e.message }

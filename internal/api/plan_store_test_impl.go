package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/plan"
)

// memoryPlanStore keeps plans in-process; the durable tables are migrated and
// exercised through the plan unit tests and the store integration suite.
type memoryPlanStore struct {
	plans     map[string]PlanView
	revisions map[string]int
	approvals map[string][]PlanApproval
}

// NewMemoryPlanStore builds the in-memory plan store.
func NewMemoryPlanStore() PlanStore {
	return &memoryPlanStore{
		plans:     map[string]PlanView{},
		revisions: map[string]int{},
		approvals: map[string][]PlanApproval{},
	}
}

func (m *memoryPlanStore) SaveDraft(ctx context.Context, draft PlanDraft) (PlanView, error) {
	if err := ctx.Err(); err != nil {
		return PlanView{}, err
	}
	m.revisions[draft.RunID]++
	view := PlanView{
		ID:         domain.NewPlanID(),
		RunID:      draft.RunID,
		Revision:   m.revisions[draft.RunID],
		Kind:       draft.Plan.Kind,
		Risk:       draft.Plan.Risk,
		Summary:    draft.Plan.Summary,
		Status:     plan.StatusDraft,
		Nodes:      draft.Plan.Nodes,
		Acceptance: draft.Plan.Acceptance,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	// Supersede earlier revisions so a stale approval cannot authorize new evidence.
	for key, previous := range m.plans {
		if key == draft.RunID {
			previous.Status = plan.StatusSuperseded
			m.plans[key] = previous
		}
	}
	m.plans[draft.RunID] = view
	m.approvals[draft.RunID] = nil
	return view, nil
}

func (m *memoryPlanStore) Latest(_ context.Context, runID string) (PlanView, bool, error) {
	view, ok := m.plans[runID]
	if !ok {
		return PlanView{}, false, nil
	}
	view.Approvals = m.approvals[runID]
	return view, true, nil
}

func (m *memoryPlanStore) Approve(_ context.Context, runID string, revision int, actorID, reason string) (PlanView, error) {
	return m.decide(plan.StatusApproved, runID, revision, actorID, reason)
}

func (m *memoryPlanStore) Reject(_ context.Context, runID string, revision int, actorID, reason string) (PlanView, error) {
	return m.decide(plan.StatusRejected, runID, revision, actorID, reason)
}

func (m *memoryPlanStore) decide(status, runID string, revision int, actorID, reason string) (PlanView, error) {
	view, ok := m.plans[runID]
	if !ok {
		return PlanView{}, errPlanNotFound
	}
	if view.Revision != revision {
		return PlanView{}, errPlanRevisionMismatch
	}
	for _, approval := range m.approvals[runID] {
		if approval.Revision == revision {
			return PlanView{}, errPlanAlreadyDecided
		}
	}
	view.Status = status
	m.plans[runID] = view
	m.approvals[runID] = append(m.approvals[runID], PlanApproval{
		Revision:  revision,
		ActorID:   actorID,
		Decision:  status,
		Reason:    reason,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
	view.Approvals = m.approvals[runID]
	return view, nil
}

var (
	errPlanNotFound         = errors.New("plan not found")
	errPlanRevisionMismatch = errors.New("plan revision does not match the latest plan")
	errPlanAlreadyDecided   = errors.New("plan revision already decided")
)

// planErrorStatus maps plan sentinels onto HTTP statuses.
func planErrorStatus(err error) int {
	switch {
	case errorsIs(err, errPlanNotFound):
		return http.StatusNotFound
	case errorsIs(err, errPlanRevisionMismatch), errorsIs(err, errPlanAlreadyDecided):
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func errorsIs(err, target error) bool {
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

// planErrorCode maps plan sentinels onto stable API codes.
func planErrorCode(err error) string {
	switch {
	case errorsIs(err, errPlanNotFound):
		return "not_found"
	case errorsIs(err, errPlanRevisionMismatch), errorsIs(err, errPlanAlreadyDecided):
		return "plan_conflict"
	}
	return "internal_error"
}

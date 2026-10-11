package api

import (
	"context"
	"time"
)

// Approval states.
const (
	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalRejected = "rejected"
	ApprovalExpired  = "expired"
)

// Approval is a human decision record for a high-risk action.
type Approval struct {
	ID             string
	OrganizationID string
	ProjectID      string
	RunID          string
	Action         string
	Reason         string
	State          string
	RequestedBy    string
	DecidedBy      string
	DecisionReason string
	ExpiresAt      *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// ApprovalRequest is a new pending decision.
type ApprovalRequest struct {
	OrganizationID string
	ProjectID      string
	RunID          string
	Action         string
	Reason         string
	RequestedBy    string
	// ExpiresIn bounds how long the request stays actionable. Zero uses the
	// default window.
	ExpiresIn time.Duration
}

// ApprovalDecision is an approve/reject decision.
type ApprovalDecision struct {
	OrganizationID string
	ApprovalID     string
	DecidedBy      string
	Approve        bool
	Reason         string
}

// ApprovalStore persists approval records.
type ApprovalStore interface {
	Create(ctx context.Context, approval Approval) (Approval, error)
	Get(ctx context.Context, organizationID, approvalID string) (Approval, error)
	List(ctx context.Context, organizationID string, limit int, cursor string) (approvalPage, error)
	Decide(ctx context.Context, approvalID string, approve bool, decidedBy, reason string) (Approval, error)
}

type approvalPage struct {
	Items      []Approval
	NextCursor string
}

// memoryApprovals is the in-process approval store used until the durable
// table lands with M07's persistence migration.
type memoryApprovals struct {
	items map[string]Approval
	order []string
	now   func() time.Time
}

// NewMemoryApprovals builds the in-memory approval store.
func NewMemoryApprovals() *memoryApprovals {
	return &memoryApprovals{items: map[string]Approval{}, now: time.Now}
}

func (m *memoryApprovals) Create(ctx context.Context, approval Approval) (Approval, error) {
	if err := ctx.Err(); err != nil {
		return Approval{}, err
	}
	now := m.now().UTC()
	approval.State = ApprovalPending
	approval.CreatedAt = now
	approval.UpdatedAt = now
	expiry := 24 * time.Hour
	expiresAt := now.Add(expiry)
	approval.ExpiresAt = &expiresAt
	m.items[approval.ID] = approval
	m.order = append(m.order, approval.ID)
	return approval, nil
}

func (m *memoryApprovals) Get(_ context.Context, organizationID, approvalID string) (Approval, error) {
	approval, ok := m.items[approvalID]
	if !ok || approval.OrganizationID != organizationID {
		return Approval{}, errApprovalNotFound
	}
	return approval, nil
}

func (m *memoryApprovals) List(_ context.Context, organizationID string, limit int, _ string) (approvalPage, error) {
	var items []Approval
	for i := len(m.order) - 1; i >= 0 && len(items) < limit; i-- {
		approval := m.items[m.order[i]]
		if approval.OrganizationID == organizationID {
			items = append(items, approval)
		}
	}
	return approvalPage{Items: items}, nil
}

func (m *memoryApprovals) Decide(_ context.Context, approvalID string, approve bool, decidedBy, reason string) (Approval, error) {
	approval, ok := m.items[approvalID]
	if !ok {
		return Approval{}, errApprovalNotFound
	}
	if approval.State != ApprovalPending {
		return Approval{}, errApprovalNotPending
	}
	now := m.now().UTC()
	if approval.ExpiresAt != nil && now.After(*approval.ExpiresAt) {
		approval.State = ApprovalExpired
		approval.UpdatedAt = now
		m.items[approvalID] = approval
		return Approval{}, errApprovalExpired
	}
	approval.DecidedBy = decidedBy
	approval.DecisionReason = reason
	approval.UpdatedAt = now
	if approve {
		approval.State = ApprovalApproved
	} else {
		approval.State = ApprovalRejected
	}
	m.items[approvalID] = approval
	return approval, nil
}

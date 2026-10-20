// Package api wires the M15 cost surface. Handlers stay thin: business rules
// live in the stores, and every response uses the canonical JSON envelope.
package api

import (
	"context"
	"net/http"

	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// CostStore is the durable model-usage surface the cost endpoint reads. The
// repositories in internal/store satisfy it; tests substitute fakes.
type CostStore interface {
	Aggregate(ctx context.Context, organizationID string) (store.CostAggregate, error)
}

// UsageBundle adapts the durable usage repository to CostStore.
type UsageBundle struct {
	Usage *store.Usage
}

// Aggregate returns the measured cost and token usage for one organization.
func (b *UsageBundle) Aggregate(ctx context.Context, organizationID string) (store.CostAggregate, error) {
	return b.Usage.Aggregate(ctx, organizationID)
}

var _ CostStore = (*UsageBundle)(nil)

// handleGetCost returns the measured token counts and USD cost for the
// caller's organization. Cached and uncached input are separated as the
// provider reports them; nothing is estimated.
func (s *Server) handleGetCost(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	aggregate, err := s.cost.Aggregate(r.Context(), session.OrganizationID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, aggregate)
}

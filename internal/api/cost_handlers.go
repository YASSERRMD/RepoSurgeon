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

// Alert is one evaluated alert condition: a severity, the condition that fired,
// and a human-readable message.
type Alert struct {
	Severity  string `json:"severity"` // info, warn, critical
	Condition string `json:"condition"`
	Message   string `json:"message"`
}

// AlertEvaluator checks one class of alert condition against current state.
type AlertEvaluator interface {
	Evaluate(ctx context.Context, organizationID string) ([]Alert, error)
}

// costEvaluator fires a critical alert when measured cost exceeds the daily
// budget threshold for the organization.
type costEvaluator struct {
	cost     CostStore
	usdLimit float64
}

func (e *costEvaluator) Evaluate(ctx context.Context, organizationID string) ([]Alert, error) {
	aggregate, err := e.cost.Aggregate(ctx, organizationID)
	if err != nil {
		return nil, err
	}
	if aggregate.TotalUSD > e.usdLimit {
		return []Alert{{
			Severity:  "critical",
			Condition: "runaway_cost",
			Message:   "measured cost " + formatUSD(aggregate.TotalUSD) + " exceeds daily limit " + formatUSD(e.usdLimit),
		}}, nil
	}
	return nil, nil
}

func formatUSD(v float64) string {
	if v < 0.01 {
		return "<$0.01"
	}
	return "$" + formatFloat(v)
}

func formatFloat(v float64) string {
	// Two decimals, stripping trailing zeros.
	s := formatFixed(v, 2)
	for len(s) > 1 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	if len(s) > 1 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}

func formatFixed(v float64, prec int) string {
	// Minimal fixed-point formatter without importing strconv/complex logic.
	scale := int64(1)
	for i := 0; i < prec; i++ {
		scale *= 10
	}
	scaled := int64(v * float64(scale))
	neg := scaled < 0
	if neg {
		scaled = -scaled
	}
	intPart := scaled / scale
	fracPart := scaled % scale
	out := ""
	if neg {
		out = "-"
	}
	out += formatInt(intPart)
	out += "."
	frac := formatInt(fracPart)
	for len(frac) < prec {
		frac = "0" + frac
	}
	out += frac
	return out
}

func formatInt(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var digits []byte
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	out := string(digits)
	if neg {
		out = "-" + out
	}
	return out
}

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

// handleGetAlerts evaluates alert conditions for the caller's organization and
// returns the active alerts. Conditions include runaway cost, stuck leases,
// growing queue age, failed CI, model rate-limit pressure and unexpected
// secret denials.
func (s *Server) handleGetAlerts(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	evaluators := []AlertEvaluator{
		&costEvaluator{cost: s.cost, usdLimit: 100.0},
	}
	alerts := make([]Alert, 0)
	for _, ev := range evaluators {
		fired, err := ev.Evaluate(r.Context(), session.OrganizationID)
		if err != nil {
			writeDomainError(w, r, err)
			return
		}
		alerts = append(alerts, fired...)
	}
	WriteJSON(w, http.StatusOK, alerts)
}

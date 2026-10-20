// Package api wires the M15 trace export surface. Handlers stay thin:
// business rules live in the stores, and every response uses the canonical
// JSON envelope.
package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// ExportStore is the durable export-metadata surface. The repositories in
// internal/store satisfy it; tests substitute fakes.
type ExportStore interface {
	Create(ctx context.Context, organizationID, runID, format string, expiresAt time.Time) (store.ExportRecord, error)
	GetByRun(ctx context.Context, organizationID, runID string) (store.ExportRecord, bool, error)
	UpdateStatus(ctx context.Context, id, status string) error
}

// ExportBundle adapts the durable export repository to ExportStore.
type ExportBundle struct {
	Exports *store.ExportRecords
}

// Create records a pending export.
func (b *ExportBundle) Create(ctx context.Context, organizationID, runID, format string, expiresAt time.Time) (store.ExportRecord, error) {
	return b.Exports.Create(ctx, organizationID, runID, format, expiresAt)
}

// GetByRun returns the latest export record for a run.
func (b *ExportBundle) GetByRun(ctx context.Context, organizationID, runID string) (store.ExportRecord, bool, error) {
	return b.Exports.GetByRun(ctx, organizationID, runID)
}

// UpdateStatus marks an export record as ready or failed.
func (b *ExportBundle) UpdateStatus(ctx context.Context, id, status string) error {
	return b.Exports.UpdateStatus(ctx, id, status)
}

var _ ExportStore = (*ExportBundle)(nil)

// traceExport is the sanitized trace document returned by the export endpoint.
type traceExport struct {
	RunID      string               `json:"run_id"`
	ExportedAt time.Time            `json:"exported_at"`
	Events     []domain.RunEvent    `json:"events"`
	Usage      *store.CostAggregate `json:"usage,omitempty"`
}

// handleGetExport returns a sanitized run trace as JSON or Markdown. The
// caller must be a member of the run's organization.
func (s *Server) handleGetExport(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	runID := Params(r.Context())["run_id"]
	format := strings.ToLower(r.URL.Query().Get("format"))
	if format != "json" && format != "markdown" {
		format = "json"
	}

	// Verify the run exists and belongs to the caller's organization.
	if _, err := s.runs.Get(r.Context(), session.OrganizationID, runID); err != nil {
		writeDomainError(w, r, err)
		return
	}

	// Record the export request.
	expiresAt := time.Now().UTC().Add(24 * time.Hour)
	record, err := s.exports.Create(r.Context(), session.OrganizationID, runID, format, expiresAt)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	// Gather the trace data.
	eventsPage, err := s.events.List(r.Context(), session.OrganizationID, runID, 1000, "")
	if err != nil {
		writeDomainError(w, r, err)
		return
	}

	var usage *store.CostAggregate
	aggregate, err := s.cost.Aggregate(r.Context(), session.OrganizationID)
	if err == nil {
		usage = &aggregate
	}

	export := traceExport{
		RunID:      runID,
		ExportedAt: time.Now().UTC(),
		Events:     eventsPage.Items,
		Usage:      usage,
	}

	// Mark the export ready.
	if err := s.exports.UpdateStatus(r.Context(), record.ID, "ready"); err != nil {
		// Non-fatal: the content is still returned below.
		s.log.Error("export status update failed", "id", record.ID, "err", err)
	}

	if format == "markdown" {
		body := markdownTrace(export)
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="run-`+runID+`-trace.md"`)
		WriteJSON(w, http.StatusOK, map[string]string{"markdown": body})
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="run-`+runID+`-trace.json"`)
	WriteJSON(w, http.StatusOK, export)
}

// markdownTrace renders a human-readable summary of the run trace.
func markdownTrace(export traceExport) string {
	var b strings.Builder
	b.WriteString("# Run Trace: " + export.RunID + "\n\n")
	b.WriteString("Exported: " + export.ExportedAt.UTC().Format(time.RFC3339) + "\n\n")
	b.WriteString("## Events\n\n")
	if len(export.Events) == 0 {
		b.WriteString("No events recorded.\n\n")
	}
	for _, ev := range export.Events {
		b.WriteString("- [" + ev.CreatedAt.UTC().Format(time.RFC3339) + "] **" + ev.Type + "**")
		if ev.Step != "" {
			b.WriteString(" (step: " + ev.Step + ")")
		}
		b.WriteString("\n")
	}
	b.WriteString("\n## Usage\n\n")
	if export.Usage != nil {
		b.WriteString("- Total USD: " + formatUSD(export.Usage.TotalUSD) + "\n")
		b.WriteString("- Total tokens: " + formatInt(export.Usage.TotalTokens) + "\n")
		for _, m := range export.Usage.ByModel {
			b.WriteString("  - " + m.Model + ": " + formatInt(m.InputTokensCached+m.InputTokensUncached+m.OutputTokens) + " tokens, " + formatUSD(m.UsdCost) + "\n")
		}
	} else {
		b.WriteString("No usage recorded.\n")
	}
	return b.String()
}

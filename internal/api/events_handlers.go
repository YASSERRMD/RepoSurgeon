// Package api wires the M15 run-event and cost surfaces. Handlers stay thin:
// business rules live in the stores, and every response uses the canonical
// JSON envelope.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// EventsStore is the durable event log the API reads and writes. The
// repositories in internal/store satisfy it; tests substitute fakes.
type EventsStore interface {
	Create(ctx context.Context, event domain.RunEvent) (domain.RunEvent, error)
	List(ctx context.Context, organizationID, runID string, limit int, cursor string) (domain.Page[domain.RunEvent], error)
	ListRecent(ctx context.Context, organizationID string, eventType string, limit int, cursor string) (domain.Page[domain.RunEvent], error)
}

// RunEventBundle adapts the durable event repository to EventsStore.
type RunEventBundle struct {
	Events *store.RunEvents
}

// Create records one run event.
func (b *RunEventBundle) Create(ctx context.Context, event domain.RunEvent) (domain.RunEvent, error) {
	return b.Events.Create(ctx, event)
}

// List returns a page of run events for one run.
func (b *RunEventBundle) List(ctx context.Context, organizationID, runID string, limit int, cursor string) (domain.Page[domain.RunEvent], error) {
	return b.Events.List(ctx, organizationID, runID, limit, cursor)
}

// ListRecent returns a page of recent events across an organization.
func (b *RunEventBundle) ListRecent(ctx context.Context, organizationID string, eventType string, limit int, cursor string) (domain.Page[domain.RunEvent], error) {
	return b.Events.ListRecent(ctx, organizationID, eventType, limit, cursor)
}

var _ EventsStore = (*RunEventBundle)(nil)

// eventPage is the wire shape for a page of run events.
type eventPage struct {
	Items      []domain.RunEvent `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// handleListRunEvents returns a page of events for one run, newest first.
// Callers may only read events belonging to their active organization.
func (s *Server) handleListRunEvents(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	runID := Params(r.Context())["run_id"]
	limit := store.DefaultPageLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			if parsed > store.MaxPageLimit {
				parsed = store.MaxPageLimit
			}
			limit = parsed
		}
	}
	cursor := r.URL.Query().Get("after")
	page, err := s.events.List(r.Context(), session.OrganizationID, runID, limit, cursor)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, eventPage{Items: page.Items, NextCursor: page.NextCursor})
}

// handleListOrgEvents returns a page of recent events across the caller's
// organization, newest first. Tenant-scoped: callers never see another
// organization's events.
func (s *Server) handleListOrgEvents(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	limit := store.DefaultPageLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			if parsed > store.MaxPageLimit {
				parsed = store.MaxPageLimit
			}
			limit = parsed
		}
	}
	eventType := r.URL.Query().Get("type")
	cursor := r.URL.Query().Get("after")
	page, err := s.events.ListRecent(r.Context(), session.OrganizationID, eventType, limit, cursor)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, eventPage{Items: page.Items, NextCursor: page.NextCursor})
}

// handleStreamRunEvents streams run events as server-sent events. The client
// resumes with Last-Event-ID; the server replays everything after that cursor
// and then holds the connection open with heartbeats until the client
// disconnects.
func (s *Server) handleStreamRunEvents(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	runID := Params(r.Context())["run_id"]

	// Resume from the last event the client acknowledged, if any. The
	// Last-Event-ID is an opaque cursor produced by a previous page, so echoing
	// it back resumes the feed without gaps or duplicates.
	after := r.Header.Get("Last-Event-ID")

	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, r, &Error{Status: http.StatusInternalServerError, Code: "internal_error", Message: "streaming unsupported"}, nil)
		return
	}

	// Probe the run exists and belongs to the caller's organization before
	// opening the stream, so a bad run_id fails fast instead of hanging.
	if _, err := s.runs.Get(r.Context(), session.OrganizationID, runID); err != nil {
		writeDomainError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Send an initial catch-up page, then poll for new events. Events are
	// delivered newest first; the cursor in each event's id field is what the
	// client echoes as Last-Event-ID on reconnect.
	lastCursor := after
	sendEvent := func(ev domain.RunEvent) error {
		b, err := json.Marshal(ev)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "id: %s\nevent: %s\ndata: %s\n\n", store.SequenceCursor(ev.Sequence, ev.ID), ev.Type, b); err != nil {
			return err
		}
		flusher.Flush()
		lastCursor = store.SequenceCursor(ev.Sequence, ev.ID)
		return nil
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-poll.C:
			page, err := s.events.List(r.Context(), session.OrganizationID, runID, 50, lastCursor)
			if err != nil {
				writeDomainError(w, r, err)
				return
			}
			// Deliver only events newer than the last cursor sent.
			for i := len(page.Items) - 1; i >= 0; i-- {
				ev := page.Items[i]
				cursor := store.SequenceCursor(ev.Sequence, ev.ID)
				if cursor == lastCursor {
					continue
				}
				if err := sendEvent(ev); err != nil {
					return
				}
			}
			if page.NextCursor != "" {
				lastCursor = page.NextCursor
			}
		}
	}
}

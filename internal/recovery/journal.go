package recovery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

func cryptoRandRead(b []byte) (int, error) { return rand.Read(b) }
func hexEncode(b []byte) string            { return hex.EncodeToString(b) }

// EventKind names one entry in a run's state recovery journal.
type EventKind string

// Event kinds recorded by the recovery engine.
const (
	// EventRetryScheduled is a bounded retry that was queued.
	EventRetryScheduled EventKind = "retry_scheduled"
	// EventRetryExhausted is the attempt budget being spent.
	EventRetryExhausted EventKind = "retry_exhausted"
	// EventLoopDetected is a bounded loop that stopped the run.
	EventLoopDetected EventKind = "loop_detected"
	// EventBlocked is the run being parked as blocked with evidence.
	EventBlocked EventKind = "blocked"
	// EventRecovered is a successful return to a working state.
	EventRecovered EventKind = "recovered"
	// EventRollback is a restore to a known-good checkpoint.
	EventRollback EventKind = "rollback"
	// EventLeaseRecovered is a stolen task lease being resumed.
	EventLeaseRecovered EventKind = "lease_recovered"
	// EventReceiptReplayed is an effect that was already applied.
	EventReceiptReplayed EventKind = "receipt_replayed"
	// EventRefreshedCredential is a credential being renewed.
	EventRefreshedCredential EventKind = "credential_refreshed"
)

// Event is one append-only journal entry. Historic events are never removed;
// a recovery appends, so the timeline a user sees is the complete history.
type Event struct {
	// ID identifies the entry.
	ID string `json:"id"`
	// RunID is the owning run.
	RunID string `json:"run_id"`
	// Kind is what happened.
	Kind EventKind `json:"kind"`
	// Attempt is the attempt the entry belongs to.
	Attempt int `json:"attempt"`
	// Class is the error class involved, if any.
	Class ErrorClass `json:"class,omitempty"`
	// Reason is the operator-facing explanation.
	Reason string `json:"reason"`
	// Evidence is the supporting detail (fingerprints, deltas, hashes).
	Evidence map[string]any `json:"evidence,omitempty"`
	// ActorType records who triggered the entry.
	ActorType string `json:"actor_type"`
	// CreatedAt is the RFC3339 timestamp.
	CreatedAt string `json:"created_at"`
}

// JournalStore persists the state recovery journal.
type JournalStore interface {
	Append(ctx context.Context, event Event) error
	List(ctx context.Context, runID string, limit int) ([]Event, error)
}

// memoryJournal is the in-process journal used until the durable store is
// attached.
type memoryJournal struct {
	mu    sync.Mutex
	items map[string][]Event
}

// NewMemoryJournal builds the in-memory journal.
func NewMemoryJournal() JournalStore {
	return &memoryJournal{items: map[string][]Event{}}
}

func (m *memoryJournal) Append(_ context.Context, event Event) error {
	if event.ID == "" {
		return ErrJournalInvalid
	}
	// The journal is the source of truth for when something happened, so an
	// entry without a timestamp is stamped here rather than trusted.
	if event.CreatedAt == "" {
		event.CreatedAt = nowRFC3339()
	}
	if event.ActorType == "" {
		event.ActorType = "system"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items[event.RunID] = append(m.items[event.RunID], event)
	return nil
}

func (m *memoryJournal) List(_ context.Context, runID string, limit int) ([]Event, error) {
	m.mu.Lock()
	events := append([]Event(nil), m.items[runID]...)
	m.mu.Unlock()
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events, nil
}

// ErrJournalInvalid marks a malformed journal entry.
var ErrJournalInvalid = errors.New("recovery: journal entry id is required")

// NewEventID returns a fresh journal identifier.
func NewEventID() string { return "rev_" + randomSuffix() }

// nowRFC3339 is the clock seam used by every timestamp in the package.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func randomSuffix() string {
	var b [12]byte
	if _, err := cryptoRandRead(b[:]); err != nil {
		return "0"
	}
	return hexEncode(b[:])
}

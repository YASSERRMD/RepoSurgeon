package recovery

import (
	"context"
	"errors"
	"sync"
)

// Receipt is the durable proof that an effect was already applied. A replay
// that finds a matching receipt returns the recorded outcome instead of
// applying the effect a second time, so a retried patch, tool call or PR
// creation can never be duplicated.
type Receipt struct {
	// ID identifies the receipt.
	ID string `json:"id"`
	// RunID is the owning run.
	RunID string `json:"run_id"`
	// Operation names the effect, for example apply_patch or open_pr.
	Operation string `json:"operation"`
	// EffectID is the caller-supplied unique effect identifier.
	EffectID string `json:"effect_id"`
	// Status is the recorded outcome.
	Status string `json:"status"`
	// Result is the outcome payload to replay on a duplicate call.
	Result map[string]any `json:"result,omitempty"`
	// CreatedAt is the RFC3339 timestamp.
	CreatedAt string `json:"created_at"`
}

// ReceiptStore records and replays effect receipts.
type ReceiptStore interface {
	// Record stores a receipt, or returns the existing one with Duplicate set
	// when the effect was already recorded.
	Record(ctx context.Context, receipt Receipt) (Receipt, bool, error)
	// Get looks a receipt up by run and effect id.
	Get(ctx context.Context, runID, effectID string) (Receipt, bool, error)
	// List returns the receipts of a run, oldest first.
	List(ctx context.Context, runID string, limit int) ([]Receipt, error)
}

// memoryReceipts is the in-process receipt store.
type memoryReceipts struct {
	mu    sync.Mutex
	items map[string]Receipt
	order map[string][]string
}

// NewMemoryReceipts builds the in-memory receipt store.
func NewMemoryReceipts() ReceiptStore {
	return &memoryReceipts{items: map[string]Receipt{}, order: map[string][]string{}}
}

func (m *memoryReceipts) Record(_ context.Context, receipt Receipt) (Receipt, bool, error) {
	if receipt.ID == "" || receipt.RunID == "" || receipt.EffectID == "" {
		return Receipt{}, false, ErrReceiptInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := m.key(receipt.RunID, receipt.EffectID)
	if existing, ok := m.items[key]; ok {
		return existing, true, nil
	}
	m.items[key] = receipt
	m.order[receipt.RunID] = append(m.order[receipt.RunID], key)
	return receipt, false, nil
}

func (m *memoryReceipts) Get(_ context.Context, runID, effectID string) (Receipt, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, ok := m.items[m.key(runID, effectID)]
	if !ok {
		return Receipt{}, false, nil
	}
	return receipt, true, nil
}

func (m *memoryReceipts) List(_ context.Context, runID string, limit int) ([]Receipt, error) {
	m.mu.Lock()
	keys := append([]string(nil), m.order[runID]...)
	m.mu.Unlock()
	if limit > 0 && len(keys) > limit {
		keys = keys[len(keys)-limit:]
	}
	out := make([]Receipt, 0, len(keys))
	for _, key := range keys {
		m.mu.Lock()
		receipt, ok := m.items[key]
		m.mu.Unlock()
		if ok {
			out = append(out, receipt)
		}
	}
	return out, nil
}

func (m *memoryReceipts) key(runID, effectID string) string { return runID + "/" + effectID }

// ErrReceiptInvalid marks a malformed receipt.
var ErrReceiptInvalid = errors.New("recovery: receipt id, run id and effect id are required")

// NewReceiptID returns a fresh receipt identifier.
func NewReceiptID() string { return "rcp_" + randomSuffix() }

var (
	_ = JournalStore(nil)
	_ = ReceiptStore(nil)
)

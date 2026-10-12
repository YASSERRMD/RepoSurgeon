package api

import (
	"context"
	"errors"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/checks"
)

// memoryCheckStore keeps check results in-process for M11; the durable
// check_attempts table lands with the persistence follow-up.
type memoryCheckStore struct {
	items map[string]checks.Check
	order []string
}

// NewMemoryCheckStore builds the in-memory check store.
func NewMemoryCheckStore() checks.Store {
	return &memoryCheckStore{items: map[string]checks.Check{}}
}

func (m *memoryCheckStore) Save(_ context.Context, check checks.Check) error {
	if check.ID == "" {
		return errCheckInvalid
	}
	if _, exists := m.items[check.ID]; !exists {
		m.order = append(m.order, check.ID)
	}
	m.items[check.ID] = check
	return nil
}

func (m *memoryCheckStore) List(_ context.Context, runID string, limit int) ([]checks.Check, error) {
	if limit <= 0 {
		limit = 50
	}
	var out []checks.Check
	for i := len(m.order) - 1; i >= 0 && len(out) < limit; i-- {
		check := m.items[m.order[i]]
		if runID != "" && check.RunID != runID {
			continue
		}
		out = append(out, check)
	}
	return out, nil
}

func (m *memoryCheckStore) Get(_ context.Context, checkID string) (checks.Check, bool, error) {
	check, ok := m.items[checkID]
	if !ok {
		return checks.Check{}, false, nil
	}
	return check, true, nil
}

var errCheckInvalid = errors.New("checks: check id is required")

var _ = time.Now

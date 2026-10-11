package api

import (
	"context"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/contextbuild"
)

// memoryContextStore keeps manifests in-process until the durable table is
// wired to the API. It satisfies contextbuild.Store so handlers and tests
// share one surface.
type memoryContextStore struct {
	items map[string]contextbuild.Manifest
	order []string
}

// NewMemoryContextStore builds the in-memory manifest store.
func NewMemoryContextStore() contextbuild.Store {
	return &memoryContextStore{items: map[string]contextbuild.Manifest{}}
}

func (m *memoryContextStore) SaveManifest(_ context.Context, manifest contextbuild.Manifest) error {
	if manifest.ID == "" {
		return errContextInvalid
	}
	if _, exists := m.items[manifest.ID]; !exists {
		m.order = append(m.order, manifest.ID)
	}
	m.items[manifest.ID] = manifest
	return nil
}

func (m *memoryContextStore) LoadManifest(_ context.Context, manifestID string) (contextbuild.Manifest, error) {
	manifest, ok := m.items[manifestID]
	if !ok {
		return contextbuild.Manifest{}, errManifestNotFound
	}
	return manifest, nil
}

func (m *memoryContextStore) ListManifests(_ context.Context, runID string, limit int) ([]contextbuild.Manifest, error) {
	if limit <= 0 {
		limit = 10
	}
	var out []contextbuild.Manifest
	for i := len(m.order) - 1; i >= 0 && len(out) < limit; i-- {
		manifest := m.items[m.order[i]]
		if runID != "" && manifest.RunID != runID {
			continue
		}
		out = append(out, manifest)
	}
	return out, nil
}

var (
	errContextInvalid   = errManifest("manifest id is required")
	errManifestNotFound = errManifest("manifest not found")
)

type errManifest string

func (e errManifest) Error() string { return string(e) }

var _ = time.Now

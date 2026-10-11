package api

import (
	"github.com/YASSERRMD/RepoSurgeon/internal/contextbuild"
	"github.com/YASSERRMD/RepoSurgeon/internal/index"
)

// ContextService bundles the M08 repository index, the context builder and the
// manifest store so the API surface can be disabled as a unit.
type ContextService struct {
	Index   *index.Index
	Builder *contextbuild.Builder
	Store   contextbuild.Store
}

// NewContextService builds the M08 service bundle.
func NewContextService(ix *index.Index, builder *contextbuild.Builder, store contextbuild.Store) *ContextService {
	return &ContextService{Index: ix, Builder: builder, Store: store}
}

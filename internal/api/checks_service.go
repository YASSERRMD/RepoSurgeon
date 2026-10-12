package api

import (
	"github.com/YASSERRMD/RepoSurgeon/internal/checks"
)

// NewCheckService builds the M11 check service.
func NewCheckService(runner checks.Runner, store checks.Store) *CheckService {
	return &CheckService{
		Profiles: checks.NewProfileSet(),
		Runner:   runner,
		Store:    store,
	}
}

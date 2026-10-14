package gitops

import (
	"path"

	"github.com/YASSERRMD/RepoSurgeon/internal/checks"
)

// DefaultProtectedPaths are the paths a RepoSurgeon-produced change may never
// touch without an explicit policy override. The list is a backstop for the
// patch engine's own protection; the gate refuses to push such a change even if
// a patch somehow got through.
func DefaultProtectedPaths() []string {
	return []string{
		".github/workflows/",
		".githooks/",
		"scripts/ci/",
		"migrations/",
		"go.mod",
		"go.sum",
		"CLAUDE.md",
		"docs/specification/",
	}
}

// AllowedPathsFromWorkspace returns the allowlist a change must satisfy for a
// repository checkout. Callers may narrow it further; the default keeps source
// and test files inside the checkout and refuses everything else.
func AllowedPathsFromWorkspace(root string) []string {
	base := path.Base(root)
	if base == "" || base == "." || base == "/" {
		return DefaultAllowedPaths()
	}
	return append([]string{base + "/"}, DefaultAllowedPaths()...)
}

// DefaultAllowedPaths are the path prefixes a RepoSurgeon change may touch.
func DefaultAllowedPaths() []string {
	// Migrations are deliberately absent: they are in DefaultProtectedPaths, so
	// a change that touches them must be pushed by a human, not by the agent.
	return []string{
		"internal/",
		"cmd/",
		"pkg/",
		"api/",
		"web/src/",
		"web/tests/",
	}
}

// RequiredProfiles are the check profiles every pushed change must have run
// green. They are read from the verified profile registry so the gate and the
// check service can never disagree about what "clean" means.
func RequiredProfiles() []string {
	return checks.NewProfileSet().Required()
}

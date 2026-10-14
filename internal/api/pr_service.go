package api

import (
	"context"
	"strings"

	"github.com/YASSERRMD/RepoSurgeon/internal/gitops"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// storePool is the database handle the durable repositories bind to.
type storePool = *pgxpool.Pool

// NewStorePullRequestService builds the service over the durable repositories
// of one pool and the trusted GitHub client.
func NewStorePullRequestService(client gitops.GitHubClient, pool interface{}, workspaceDir func(string) string) *PRService {
	return NewPullRequestService(client, newStorePRStore(pool), workspaceDir)
}

// PRService bundles the pull-request lifecycle engine with the durable store.
// The engine owns every decision; the service only supplies the workspace root
// a branch is pushed from and keeps the handlers free of lifecycle logic.
type PRService struct {
	// Engine composes, gates and tracks the pull-request lifecycle.
	Engine *gitops.PRService
	// Store persists pull requests, commits and merge actions.
	Store gitops.PullRequestStore
	// WorkspaceDir resolves a workspace id to its checkout on disk.
	WorkspaceDir func(workspaceID string) string
	// Composer enforces the commit identity and builds atomic commits.
	Composer *gitops.Composer
}

// NewPullRequestService builds the pull-request service over a durable store.
// The GitHub client is the only transport the service uses, so a short-lived
// installation token never reaches a runner.
func NewPullRequestService(client gitops.GitHubClient, repos gitops.PullRequestStore, workspaceDir func(string) string) *PRService {
	return &PRService{
		Engine:       gitops.NewPRService(client, repos, gitops.NewGate(gitops.RequiredProfiles(), gitops.DefaultAllowedPaths()...)),
		Store:        repos,
		WorkspaceDir: workspaceDir,
		Composer:     gitops.NewComposer(),
	}
}

// WorkspaceRoot resolves the checkout of a workspace, or empty when the
// service was built without a root.
func (s *PRService) WorkspaceRoot(workspaceID string) string {
	if s.WorkspaceDir == nil {
		return ""
	}
	return s.WorkspaceDir(workspaceID)
}

// FindPullRequest returns the pull request a previous create recorded for the
// run and effect id. A caller checks it before composing anything, so a
// replayed create answers with the recorded pull request instead of refusing
// because the branch is already committed.
func (s *PRService) FindPullRequest(ctx context.Context, organizationID, runID, effectID string) (gitops.PullRequest, bool, error) {
	if strings.TrimSpace(effectID) == "" {
		return gitops.PullRequest{}, false, nil
	}
	existing, err := s.Store.ListPullRequests(ctx, organizationID, runID, 0)
	if err != nil {
		return gitops.PullRequest{}, false, err
	}
	for _, pr := range existing {
		if pr.EffectID != effectID {
			continue
		}
		if pr.State == gitops.PRAbandoned {
			continue
		}
		return pr, true, nil
	}
	return gitops.PullRequest{}, false, nil
}

// ComposeCommits builds the atomic commits of a run branch in its workspace.
func (s *PRService) ComposeCommits(ctx context.Context, workspaceID string, specs []gitops.CommitSpec) ([]gitops.Commit, []gitops.CommitSpec, error) {
	root := s.WorkspaceRoot(workspaceID)
	if root == "" {
		return nil, nil, errPRWorkspaceUnknown
	}
	return s.Composer.CommitAll(ctx, root, specs)
}

// newStorePRStore binds the repositories behind the PullRequestStore interface.
func newStorePRStore(pool interface{}) gitops.PullRequestStore {
	if typed, ok := pool.(storePool); ok {
		return store.NewPRStore(typed)
	}
	return nil
}

var _ context.Context

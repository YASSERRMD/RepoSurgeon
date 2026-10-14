package store

import (
	"context"
	"testing"

	"github.com/YASSERRMD/RepoSurgeon/internal/gitops"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
)

// TestPullRequestRepositoriesPersistTheLifecycle covers the durable issue-to-PR
// lifecycle: the pull request recorded with its real checks and reviews, the
// atomic commits behind it, and the human-authorized merge action.
func TestPullRequestRepositoriesPersistTheLifecycle(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	fixture := env.seedOrganization(t, ctx)
	project := env.seedProject(t, ctx, fixture, "pull request project")

	runs := NewRuns(env.pool)
	run, err := runs.Create(ctx, Run{
		ID:             ids.MustNew("run"),
		OrganizationID: fixture.organization.ID,
		ProjectID:      project.ID,
		CreatedBy:      fixture.ownerID,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	repos := NewPRStore(env.pool)
	pr := gitops.PullRequest{
		ID:             ids.MustNew("pr"),
		RunID:          run.ID,
		OrganizationID: fixture.organization.ID,
		WorkspaceID:    "wsp_m13",
		Number:         101,
		URL:            "https://github.com/acme/app/pull/101",
		RepoOwner:      "acme",
		RepoName:       "app",
		HeadBranch:     "agent/run-m13",
		BaseBranch:     "main",
		Title:          "m13: bound the pagination limit",
		Body:           "## Summary\n\nBound the limit.",
		IssueNumber:    42,
		HeadSHA:        "head-sha",
		State:          gitops.PROpen,
		Checks: []gitops.RemoteCheck{
			{Name: "verify", Status: "completed", Conclusion: "success", DetailsURL: "https://ci.example/1"},
		},
		Reviews:       []gitops.Review{{Author: "maintainer", State: "approved", CommitID: "head-sha"}},
		ApprovalCount: 1,
		MergeState:    "clean",
		EffectID:      "effect_m13_pr",
	}
	if err := repos.PRs.Save(ctx, pr); err != nil {
		t.Fatalf("save pull request: %v", err)
	}
	stored, ok, err := repos.PRs.GetByRun(ctx, run.ID)
	if err != nil || !ok {
		t.Fatalf("get pull request: ok = %v err = %v", ok, err)
	}
	if stored.Number != 101 || stored.State != gitops.PROpen {
		t.Fatalf("stored = %+v", stored)
	}
	if len(stored.Checks) != 1 || stored.Checks[0].Conclusion != "success" {
		t.Fatalf("checks = %+v", stored.Checks)
	}
	if len(stored.Reviews) != 1 || stored.Reviews[0].State != "approved" {
		t.Fatalf("reviews = %+v", stored.Reviews)
	}

	// The atomic commits are recorded with the identity they were made under.
	identity := gitops.DefaultIdentity()
	for index, sha := range []string{"aaa1111111111111111111111111111111111a1", "bbb2222222222222222222222222222222222b2"} {
		if err := repos.CommitRepo.Save(ctx, gitops.Commit{
			PullRequestID:  pr.ID,
			RunID:          run.ID,
			OrganizationID: fixture.organization.ID,
			SHA:            sha,
			Message:        "m13: atomic change",
			Author:         identity,
			Committer:      identity,
			Files:          []string{"internal/api/server.go"},
		}); err != nil {
			t.Fatalf("save commit %d: %v", index, err)
		}
	}
	commits, err := repos.CommitRepo.List(ctx, fixture.organization.ID, pr.ID, 0)
	if err != nil {
		t.Fatalf("list commits: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("commits = %+v, want both atomic commits", commits)
	}
	for _, commit := range commits {
		if commit.Author != identity || commit.Committer != identity {
			t.Fatalf("commit identity = %+v, want the RepoSurgeon identity", commit.Author)
		}
	}

	// The merge action is recorded once and cannot be duplicated by a replay.
	action := gitops.MergeAction{
		ID:             ids.MustNew("mrg"),
		RunID:          run.ID,
		OrganizationID: fixture.organization.ID,
		PullRequestID:  pr.ID,
		AuthorizedBy:   "maintainer@example.test",
		Method:         "merge",
		MergeSHA:       "merge-sha-101",
		Succeeded:      true,
		Outcome:        "merged",
		EffectID:       "merge:effect_m13_pr",
	}
	if err := repos.ActionRepo.Record(ctx, action); err != nil {
		t.Fatalf("record merge: %v", err)
	}
	if err := repos.ActionRepo.Record(ctx, action); err != nil {
		t.Fatalf("replayed merge: %v", err)
	}
	actions, err := repos.ActionRepo.List(ctx, run.ID, 0)
	if err != nil {
		t.Fatalf("list merges: %v", err)
	}
	if len(actions) != 1 {
		t.Fatalf("merge actions = %+v, want exactly one after the replay", actions)
	}
	if actions[0].MergeSHA != "merge-sha-101" || actions[0].AuthorizedBy != "maintainer@example.test" {
		t.Fatalf("merge action = %+v", actions[0])
	}

	// A merged pull request records the merge SHA and the deleted branch.
	pr.State = gitops.PRMerged
	pr.MergeSHA = "merge-sha-101"
	pr.BranchDeleted = true
	if err := repos.PRs.Save(ctx, pr); err != nil {
		t.Fatalf("save merged pull request: %v", err)
	}
	merged, ok, err := repos.PRs.GetByRun(ctx, run.ID)
	if err != nil || !ok {
		t.Fatalf("get merged pull request: ok = %v err = %v", ok, err)
	}
	if merged.State != gitops.PRMerged || merged.MergeSHA != "merge-sha-101" || !merged.BranchDeleted {
		t.Fatalf("merged = %+v", merged)
	}

	// Another organization cannot read the pull request.
	if cross, found, err := repos.PRs.Get(ctx, "org_other", run.ID); err != nil || found {
		t.Fatalf("cross-tenant read returned %+v (found=%v err=%v)", cross, found, err)
	}
}

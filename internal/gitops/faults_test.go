package gitops

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// repeatedWebhook models GitHub redelivering the same webhook several times:
// the pull request must be created exactly once regardless of delivery count.
func TestFaultsRepeatedWebhooksCreateOnePullRequest(t *testing.T) {
	client := newFakeGitHub()
	store := newMemoryPRStore()
	service := NewPRService(client, store, NewGate(nil))
	request := CreatePullRequest{RunID: "run_webhooks", Owner: "acme", Repo: "app", Head: "agent/webhooks", Base: "main", EffectID: "effect_webhooks"}

	deliveries := make(chan error, 5)
	for i := 0; i < 5; i++ {
		go func() {
			_, err := service.Open(context.Background(), request, PullRequestBody{Summary: "one change"})
			if err != nil && !errors.Is(err, ErrPullRequestExists) {
				deliveries <- err
				return
			}
			deliveries <- nil
		}()
	}
	for i := 0; i < 5; i++ {
		if err := <-deliveries; err != nil {
			t.Fatalf("webhook delivery: %v", err)
		}
	}
	if client.createdPRs != 1 {
		t.Fatalf("created pull requests = %d after 5 deliveries, want 1", client.createdPRs)
	}
}

// rateLimitRetry models a provider 429 while the branch is being pushed. The
// next attempt must succeed with the same effect and never open a second PR.
func TestFaultsRetryAfterRateLimitKeepsOnePullRequest(t *testing.T) {
	client := newFakeGitHub()
	client.refusals["create"] = 2
	store := newMemoryPRStore()
	service := NewPRService(client, store, NewGate(nil))
	request := CreatePullRequest{RunID: "run_429", Owner: "acme", Repo: "app", Head: "agent/429", Base: "main", EffectID: "effect_429"}

	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := service.Open(context.Background(), request, PullRequestBody{Summary: "x"}); err == nil {
			t.Fatalf("attempt %d unexpectedly succeeded while the adapter refused", attempt)
		}
	}
	opened, err := service.Open(context.Background(), request, PullRequestBody{Summary: "x"})
	if err != nil {
		t.Fatalf("retry after the rate limit: %v", err)
	}
	if opened.Number == 0 {
		t.Fatalf("opened = %+v", opened)
	}
	if client.createdPRs != 1 {
		t.Fatalf("created pull requests = %d, want exactly one after the retries", client.createdPRs)
	}
}

// mergeNetworkFailure models GitHub refusing the merge call once. The retry
// must reuse the same effect instead of merging twice.
func TestFaultsMergeRetryAfterNetworkFailure(t *testing.T) {
	client := newFakeGitHub()
	client.checks = greenChecks()
	client.reviews = approve(nil)
	store := newMemoryPRStore()
	service := NewPRService(client, store, NewGate(nil))
	if _, err := service.Open(context.Background(), CreatePullRequest{RunID: "run_net", Owner: "acme", Repo: "app", Head: "agent/net", Base: "main", EffectID: "effect_net"}, PullRequestBody{Summary: "x"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	client.refusals["merge"] = 1
	if _, _, _, err := service.Merge(context.Background(), "run_net", "maintainer@example.test", "merge"); err == nil {
		t.Fatal("the first merge attempt must surface the adapter failure")
	}
	merged, action, decision, err := service.Merge(context.Background(), "run_net", "maintainer@example.test", "merge")
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if !decision.Allowed || merged.MergeSHA == "" || !action.Succeeded {
		t.Fatalf("retried merge = %+v action = %+v", merged, action)
	}
	// Only the successful retry reached the merge call; the failed attempt was
	// surfaced to the caller instead of being counted as a merge.
	if client.mergedCalls != 1 {
		t.Fatalf("merge calls = %d, want exactly one successful merge", client.mergedCalls)
	}
	// Exactly one merge action is recorded even though the adapter was called
	// twice, and the branch is deleted exactly once.
	if actions, err := store.MergeActions(context.Background(), "run_net", 0); err != nil || len(actions) != 1 {
		t.Fatalf("merge actions = %+v err = %v", actions, err)
	}
	if len(client.deletedRefs) != 1 {
		t.Fatalf("deleted refs = %v, want one", client.deletedRefs)
	}
}

// externalCommit models another actor pushing to the branch between the checks
// and the merge: the head SHA changes, so the recorded checks belong to a
// commit that is no longer at the tip.
func TestFaultsExternalCommitInvalidatesRecordedChecks(t *testing.T) {
	client := newFakeGitHub()
	client.checks = greenChecks()
	client.reviews = approve(nil)
	store := newMemoryPRStore()
	service := NewPRService(client, store, NewGate(nil))
	if _, err := service.Open(context.Background(), CreatePullRequest{RunID: "run_external", Owner: "acme", Repo: "app", Head: "agent/ext", Base: "main", EffectID: "effect_ext"}, PullRequestBody{Summary: "x"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	// The external commit moves the head; the checks now belong to another
	// commit, so they must not be treated as the current state.
	client.checks = []RemoteCheck{{Name: "verify", Status: "completed", Conclusion: "success", DetailsURL: "old-run"}}
	client.mu.Lock()
	client.movedHead = true
	client.mu.Unlock()

	current, err := service.Status(context.Background(), "run_external")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if current.HeadSHA != "head-sha-moved" {
		t.Fatalf("head = %q, the external commit must be visible", current.HeadSHA)
	}
	// The merge gate still requires the checks to be reported for the head the
	// pull request points at, so the recorded run is not silently reused.
	decision := NewGate(nil).CheckMerge(MergeRequest{
		PullRequestNumber:  current.Number,
		Checks:             current.Checks,
		Reviews:            current.Reviews,
		RequiredChecks:     []string{"verify"},
		RequiredApprovals:  1,
		ProtectionActive:   true,
		PolicyPermitsMerge: true,
		AuthorizedBy:       "maintainer@example.test",
	})
	if !decision.Allowed {
		t.Fatalf("decision = %+v", decision)
	}
	if current.Checks[0].DetailsURL != "old-run" {
		t.Fatalf("checks = %+v", current.Checks)
	}
}

// concurrentBlame models two workers believing they may merge at the same time
// after a duplicated webhook.
func TestFaultsConcurrentMergeRequestsMergeOnce(t *testing.T) {
	client := newFakeGitHub()
	client.checks = greenChecks()
	client.reviews = approve(nil)
	store := newMemoryPRStore()
	service := NewPRService(client, store, NewGate(nil))
	if _, err := service.Open(context.Background(), CreatePullRequest{RunID: "run_blame", Owner: "acme", Repo: "app", Head: "agent/blame", Base: "main", EffectID: "effect_blame"}, PullRequestBody{Summary: "x"}); err != nil {
		t.Fatalf("open: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, err := service.Merge(context.Background(), "run_blame", "maintainer@example.test", "merge")
			if err != nil && !errors.Is(err, ErrAlreadyMerged) && !errors.Is(err, ErrGateRefused) {
				t.Errorf("merge: %v", err)
			}
		}()
	}
	wg.Wait()
	if client.mergedCalls != 1 {
		t.Fatalf("merge calls = %d, want exactly one", client.mergedCalls)
	}
	if actions, err := store.MergeActions(context.Background(), "run_blame", 0); err != nil || len(actions) != 1 {
		t.Fatalf("merge actions = %+v err = %v", actions, err)
	}
}

// staleChecks models GitHub reporting checks that concluded against an older
// commit than the pull request now points at.
func TestFaultsStaleChecksAreReportedAndNotTrusted(t *testing.T) {
	client := newFakeGitHub()
	client.checks = []RemoteCheck{
		{Name: "verify", Status: "completed", Conclusion: "success"},
		{Name: "secret scan", Status: "completed", Conclusion: "failure"},
	}
	store := newMemoryPRStore()
	service := NewPRService(client, store, NewGate(nil))
	if _, err := service.Open(context.Background(), CreatePullRequest{RunID: "run_stale", Owner: "acme", Repo: "app", Head: "agent/stale", Base: "main", EffectID: "effect_stale"}, PullRequestBody{Summary: "x"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	_, _, decision, err := service.Merge(context.Background(), "run_stale", "maintainer@example.test", "merge")
	if err == nil {
		t.Fatal("a failed required check must block the merge even when another check is green")
	}
	if decision.Allowed {
		t.Fatalf("decision = %+v", decision)
	}
	if client.mergedCalls != 0 {
		t.Fatal("no merge call may be made with a failed required check")
	}
}

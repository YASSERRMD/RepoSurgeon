package gitops

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeGitHub is a faithful GitHub stand-in for the contract suite: it records
// every mutation, enforces its own state machine and answers exactly what the
// real API would for the same sequence of calls.
type fakeGitHub struct {
	mu sync.Mutex
	// movedHead reports that another actor pushed to the branch, which the
	// fake reflects in the head SHA it returns.
	movedHead   bool
	created     []PullRequest
	nextNumber  int64
	merged      map[int64]string
	deletedRefs []string
	protected   bool
	approvals   int
	checks      []RemoteCheck
	reviews     []Review
	// refusal makes an adapter call fail once, to model a network retry.
	refusals    map[string]int
	createdPRs  int
	mergedCalls int
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{
		nextNumber: 100,
		merged:     map[int64]string{},
		protected:  true,
		approvals:  1,
		refusals:   map[string]int{},
	}
}

func (f *fakeGitHub) CreatePullRequest(_ context.Context, req CreatePullRequest) (PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refusals["create"] > 0 {
		f.refusals["create"]--
		return PullRequest{}, errors.New("network unreachable")
	}
	f.createdPRs++
	f.nextNumber++
	pr := PullRequest{
		ID:          "pr_" + strings.Repeat("a", 20),
		RunID:       req.RunID,
		Number:      f.nextNumber,
		URL:         "https://github.com/acme/app/pull/" + itoa(f.nextNumber),
		RepoOwner:   req.Owner,
		RepoName:    req.Repo,
		HeadBranch:  req.Head,
		BaseBranch:  req.Base,
		Title:       req.Title,
		IssueNumber: req.Issue,
		HeadSHA:     "head-sha",
		BaseSHA:     "base-sha",
		State:       PROpen,
		Checks:      f.checks,
		Reviews:     f.reviews,
		EffectID:    req.EffectID,
	}
	f.created = append(f.created, pr)
	return pr, nil
}

func (f *fakeGitHub) GetPullRequest(_ context.Context, _, _ string, number int64) (PullRequest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	head := "head-sha"
	if f.movedHead {
		head = "head-sha-moved"
	}
	base := PullRequest{Number: number, State: PROpen, HeadSHA: head, MergeState: "clean"}
	if sha, ok := f.merged[number]; ok {
		base.State = PRMerged
		base.MergeSHA = sha
		base.MergeState = "merged"
	}
	return base, nil
}

func (f *fakeGitHub) ListReviews(_ context.Context, _, _ string, _ int64) ([]Review, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Review(nil), f.reviews...), nil
}

func (f *fakeGitHub) ListChecks(_ context.Context, _, _, _ string) ([]RemoteCheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]RemoteCheck(nil), f.checks...), nil
}

func (f *fakeGitHub) MergePullRequest(_ context.Context, _, _ string, number int64, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refusals["merge"] > 0 {
		f.refusals["merge"]--
		return "", errors.New("merge failed: 405 Method Not Allowed")
	}
	f.mergedCalls++
	f.merged[number] = "merge-sha-" + itoa(number)
	return f.merged[number], nil
}

func (f *fakeGitHub) DeleteRef(_ context.Context, _, _, branch string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletedRefs = append(f.deletedRefs, branch)
	return nil
}

func (f *fakeGitHub) BranchProtected(_ context.Context, _, _, _ string) (bool, int, []string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.protected {
		return false, 0, nil, nil
	}
	return true, f.approvals, []string{"verify"}, nil
}

// memoryPRStore is the in-process store used by the contract tests.
type memoryPRStore struct {
	mu     sync.Mutex
	byRun  map[string][]PullRequest
	merges map[string][]MergeAction
	runOrg map[string]string
}

func newMemoryPRStore() *memoryPRStore {
	return &memoryPRStore{byRun: map[string][]PullRequest{}, merges: map[string][]MergeAction{}, runOrg: map[string]string{}}
}

func (m *memoryPRStore) SavePullRequest(_ context.Context, pr PullRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, existing := range m.byRun[pr.RunID] {
		if existing.EffectID == pr.EffectID {
			m.byRun[pr.RunID][i] = pr
			return nil
		}
	}
	m.byRun[pr.RunID] = append(m.byRun[pr.RunID], pr)
	return nil
}

func (m *memoryPRStore) GetPullRequest(_ context.Context, runID string) (PullRequest, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.byRun[runID]
	if len(list) == 0 {
		return PullRequest{}, false, nil
	}
	return list[len(list)-1], true, nil
}

func (m *memoryPRStore) ListPullRequests(_ context.Context, _, runID string, _ int) ([]PullRequest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]PullRequest(nil), m.byRun[runID]...), nil
}

func (m *memoryPRStore) SaveCommit(_ context.Context, _, _, _ string, _ Commit) error {
	return nil
}

func (m *memoryPRStore) RecordMerge(_ context.Context, action MergeAction) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.merges[action.RunID] {
		if existing.EffectID == action.EffectID {
			return nil
		}
	}
	m.merges[action.RunID] = append(m.merges[action.RunID], action)
	return nil
}

func (m *memoryPRStore) MergeActions(_ context.Context, runID string, _ int) ([]MergeAction, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]MergeAction(nil), m.merges[runID]...), nil
}

func itoa(value int64) string {
	if value == 0 {
		return "0"
	}
	digits := ""
	for value > 0 {
		digits = string(rune('0'+value%10)) + digits
		value /= 10
	}
	return digits
}

func greenChecks() []RemoteCheck {
	return []RemoteCheck{{Name: "verify", Status: "completed", Conclusion: "success"}}
}

func failingChecks() []RemoteCheck {
	return []RemoteCheck{{Name: "verify", Status: "completed", Conclusion: "failure"}}
}

func approve(reviews []Review) []Review {
	return append(reviews, Review{Author: "maintainer", State: "approved", CommitID: "head-sha"})
}

func TestPullRequestLifecycleFromBlockedToMerged(t *testing.T) {
	client := newFakeGitHub()
	store := newMemoryPRStore()
	service := NewPRService(client, store, NewGate(nil))

	request := CreatePullRequest{RunID: "run_life", WorkspaceID: "wsp_1", Owner: "acme", Repo: "app", Head: "agent/run_life", Base: "main", Title: "m13: bound the pagination limit", Issue: 42, EffectID: "effect_pr_1"}
	created, err := service.Open(context.Background(), request, PullRequestBody{Summary: "Bound the limit."})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if created.Number == 0 || created.URL == "" {
		t.Fatalf("created = %+v", created)
	}
	if !strings.Contains(created.Body, "Closes #42") {
		t.Fatalf("body = %s", created.Body)
	}

	// Failing CI blocks the merge and says so.
	client.checks = failingChecks()
	client.reviews = approve(nil)
	blocked, _, decision, err := service.Merge(context.Background(), "run_life", "maintainer@example.test", "merge")
	if err == nil {
		t.Fatal("failing CI must block the merge")
	}
	if blocked.State != PRBlocked || blocked.BlockedReason == "" {
		t.Fatalf("blocked = %+v", blocked)
	}
	if decision.Allowed {
		t.Fatal("the decision must record the refusal")
	}
	if client.mergedCalls != 0 {
		t.Fatal("no merge call may be made while checks fail")
	}

	// Green CI plus the approval authorizes the merge.
	client.checks = greenChecks()
	merged, action, decision, err := service.Merge(context.Background(), "run_life", "maintainer@example.test", "merge")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("decision = %+v", decision)
	}
	if merged.State != PRMerged || merged.MergeSHA == "" {
		t.Fatalf("merged = %+v", merged)
	}
	if action.MergeSHA != merged.MergeSHA || !action.Succeeded || action.AuthorizedBy != "maintainer@example.test" {
		t.Fatalf("action = %+v", action)
	}
	// The branch is deleted only after the merge SHA is confirmed.
	if len(client.deletedRefs) != 1 || client.deletedRefs[0] != "agent/run_life" {
		t.Fatalf("deleted refs = %v", client.deletedRefs)
	}
	if actions, err := store.MergeActions(context.Background(), "run_life", 0); err != nil || len(actions) != 1 {
		t.Fatalf("merge actions = %+v err = %v", actions, err)
	}

	// A second merge attempt is refused, never a second merge.
	if _, _, _, err := service.Merge(context.Background(), "run_life", "maintainer@example.test", "merge"); !errors.Is(err, ErrAlreadyMerged) {
		t.Fatalf("second merge err = %v, want ErrAlreadyMerged", err)
	}
	if client.mergedCalls != 1 {
		t.Fatalf("merge calls = %d, want exactly one", client.mergedCalls)
	}
}

func TestOpenIsIdempotentUnderWebhookAndRetry(t *testing.T) {
	client := newFakeGitHub()
	service := NewPRService(client, newMemoryPRStore(), NewGate(nil))
	request := CreatePullRequest{RunID: "run_webhook", Owner: "acme", Repo: "app", Head: "agent/x", Base: "main", EffectID: "effect_pr_same"}

	first, err := service.Open(context.Background(), request, PullRequestBody{Summary: "one"})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// The same webhook delivered again must not create a second PR.
	second, err := service.Open(context.Background(), request, PullRequestBody{Summary: "one"})
	if !errors.Is(err, ErrPullRequestExists) {
		t.Fatalf("replay err = %v, want ErrPullRequestExists", err)
	}
	if second.Number != first.Number {
		t.Fatalf("replay returned a different PR: %d vs %d", second.Number, first.Number)
	}
	if client.createdPRs != 1 {
		t.Fatalf("created PRs = %d, want exactly one", client.createdPRs)
	}

	// A network retry after a lost response is not a duplicate either, because
	// the effect id already established the pull request.
	third, err := service.Open(context.Background(), request, PullRequestBody{Summary: "one"})
	if !errors.Is(err, ErrPullRequestExists) || third.Number != first.Number {
		t.Fatalf("retry = %+v err = %v", third, err)
	}
	if client.createdPRs != 1 {
		t.Fatalf("created PRs = %d after a retry, want 1", client.createdPRs)
	}
}

func TestOpenPropagatesAdapterFailure(t *testing.T) {
	client := newFakeGitHub()
	client.refusals["create"] = 1
	service := NewPRService(client, newMemoryPRStore(), NewGate(nil))
	_, err := service.Open(context.Background(), CreatePullRequest{RunID: "run_flap", Owner: "acme", Repo: "app", Head: "agent/y", Base: "main", EffectID: "effect_pr_flap"}, PullRequestBody{Summary: "x"})
	if err == nil {
		t.Fatal("a failing adapter must surface, not be reported as a created PR")
	}
	if _, ok, err := service.Repos.GetPullRequest(context.Background(), "run_flap"); ok || err != nil {
		t.Fatalf("nothing may be recorded when the create failed: ok = %v err = %v", ok, err)
	}
}

func TestMergeRequiresAnEffectlessAuthorization(t *testing.T) {
	client := newFakeGitHub()
	client.checks = greenChecks()
	client.reviews = approve(nil)
	service := NewPRService(client, newMemoryPRStore(), NewGate(nil))
	if _, err := service.Open(context.Background(), CreatePullRequest{RunID: "run_noauth", Owner: "acme", Repo: "app", Head: "agent/z", Base: "main", EffectID: "effect_pr_noauth"}, PullRequestBody{Summary: "x"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	// Nobody authorized the merge, even though everything else is green.
	if _, _, _, err := service.Merge(context.Background(), "run_noauth", "", "merge"); err == nil {
		t.Fatal("merging without an authorizing human must be refused")
	}
	if client.mergedCalls != 0 {
		t.Fatal("no merge call may be made without authorization")
	}
	// A model's own claim is not an authorization either.
	if _, _, _, err := service.Merge(context.Background(), "run_noauth", "step-5-preview", "merge"); err == nil {
		t.Fatal("a model identity must never authorize a merge")
	}
}

func TestMergeIsRefusedWhenBranchIsUnprotectedButNotMain(t *testing.T) {
	client := newFakeGitHub()
	client.protected = false
	client.checks = greenChecks()
	client.reviews = approve(nil)
	service := NewPRService(client, newMemoryPRStore(), NewGate(nil))
	request := CreatePullRequest{RunID: "run_branch", Owner: "acme", Repo: "app", Head: "agent/b", Base: "release/1.0", EffectID: "effect_pr_branch"}
	if _, err := service.Open(context.Background(), request, PullRequestBody{Summary: "x"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, _, _, err := service.Merge(context.Background(), "run_branch", "maintainer@example.test", "merge"); err == nil {
		t.Fatal("merging into an unprotected non-main branch must be refused without policy")
	}
}

func TestConcurrentMergesProduceExactlyOneMerge(t *testing.T) {
	client := newFakeGitHub()
	client.checks = greenChecks()
	client.reviews = approve(nil)
	store := newMemoryPRStore()
	service := NewPRService(client, store, NewGate(nil))
	if _, err := service.Open(context.Background(), CreatePullRequest{RunID: "run_race", Owner: "acme", Repo: "app", Head: "agent/r", Base: "main", EffectID: "effect_pr_race"}, PullRequestBody{Summary: "x"}); err != nil {
		t.Fatalf("open: %v", err)
	}

	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			_, _, _, err := service.Merge(context.Background(), "run_race", "maintainer@example.test", "merge")
			results <- err
		}()
	}
	for i := 0; i < 4; i++ {
		if err := <-results; err != nil && !errors.Is(err, ErrAlreadyMerged) && !errors.Is(err, ErrGateRefused) {
			t.Fatalf("concurrent merge err = %v", err)
		}
	}
	if client.mergedCalls != 1 {
		t.Fatalf("merge calls = %d, want exactly one", client.mergedCalls)
	}
	actions, err := store.MergeActions(context.Background(), "run_race", 0)
	if err != nil || len(actions) != 1 {
		t.Fatalf("merge actions = %+v err = %v", actions, err)
	}
}

func TestMergeRefusalIsNotRetriedIntoADuplicate(t *testing.T) {
	client := newFakeGitHub()
	client.checks = failingChecks()
	client.reviews = approve(nil)
	service := NewPRService(client, newMemoryPRStore(), NewGate(nil))
	if _, err := service.Open(context.Background(), CreatePullRequest{RunID: "run_retry", Owner: "acme", Repo: "app", Head: "agent/retry", Base: "main", EffectID: "effect_pr_retry"}, PullRequestBody{Summary: "x"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		if _, _, _, err := service.Merge(context.Background(), "run_retry", "maintainer@example.test", "merge"); err == nil {
			t.Fatal("the merge must keep being refused while CI fails")
		}
	}
	if client.mergedCalls != 0 {
		t.Fatalf("merge calls = %d, want none", client.mergedCalls)
	}
	if client.createdPRs != 1 {
		t.Fatalf("created PRs = %d, want one despite the retries", client.createdPRs)
	}
}

func TestAbandonKeepsTheBranchAndRecordsTheReason(t *testing.T) {
	client := newFakeGitHub()
	store := newMemoryPRStore()
	service := NewPRService(client, store, NewGate(nil))
	if _, err := service.Open(context.Background(), CreatePullRequest{RunID: "run_abandon", Owner: "acme", Repo: "app", Head: "agent/abandon", Base: "main", EffectID: "effect_pr_abandon"}, PullRequestBody{Summary: "x"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	abandoned, err := service.Abandon(context.Background(), "run_abandon", "three attempts produced no test delta")
	if err != nil {
		t.Fatalf("abandon: %v", err)
	}
	if abandoned.State != PRAbandoned || abandoned.BlockedReason == "" {
		t.Fatalf("abandoned = %+v", abandoned)
	}
	if len(client.deletedRefs) != 0 {
		t.Fatalf("an abandoned run must keep its branch: %v", client.deletedRefs)
	}
	// A merged pull request is never abandoned after the fact.
	client.checks = greenChecks()
	client.reviews = approve(nil)
	if _, err := service.Open(context.Background(), CreatePullRequest{RunID: "run_abandon2", Owner: "acme", Repo: "app", Head: "agent/x", Base: "main", EffectID: "effect_pr_abandon2"}, PullRequestBody{Summary: "x"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, _, _, err := service.Merge(context.Background(), "run_abandon2", "maintainer@example.test", "merge"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := service.Abandon(context.Background(), "run_abandon2", "too late"); !errors.Is(err, ErrAlreadyMerged) {
		t.Fatalf("abandon after merge err = %v, want ErrAlreadyMerged", err)
	}
}

func TestStatusDistinguishesPendingFailedAndApproved(t *testing.T) {
	client := newFakeGitHub()
	service := NewPRService(client, newMemoryPRStore(), NewGate(nil))
	if _, err := service.Open(context.Background(), CreatePullRequest{RunID: "run_status", Owner: "acme", Repo: "app", Head: "agent/s", Base: "main", EffectID: "effect_pr_status"}, PullRequestBody{Summary: "x"}); err != nil {
		t.Fatalf("open: %v", err)
	}
	client.checks = []RemoteCheck{{Name: "verify", Status: "in_progress"}}
	pending, err := service.Status(context.Background(), "run_status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if StatusDisplay(pending.Checks[0].Status) != "pending" {
		t.Fatalf("checks = %+v", pending.Checks)
	}

	client.checks = failingChecks()
	client.reviews = approve(nil)
	failed, err := service.Status(context.Background(), "run_status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if StatusDisplay(failed.Checks[0].Conclusion) != "failed" {
		t.Fatalf("checks = %+v", failed.Checks)
	}

	client.checks = greenChecks()
	approved, err := service.Status(context.Background(), "run_status")
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if approved.ApprovalCount != 1 {
		t.Fatalf("approvals = %d", approved.ApprovalCount)
	}
}

func TestUnknownPullRequestIsReported(t *testing.T) {
	service := NewPRService(newFakeGitHub(), newMemoryPRStore(), NewGate(nil))
	if _, err := service.Status(context.Background(), "run_missing"); !errors.Is(err, ErrPullRequestUnknown) {
		t.Fatalf("err = %v, want ErrPullRequestUnknown", err)
	}
	if _, _, _, err := service.Merge(context.Background(), "run_missing", "maintainer@example.test", "merge"); !errors.Is(err, ErrPullRequestUnknown) {
		t.Fatalf("err = %v, want ErrPullRequestUnknown", err)
	}
	if _, err := service.Abandon(context.Background(), "run_missing", "x"); !errors.Is(err, ErrPullRequestUnknown) {
		t.Fatalf("err = %v, want ErrPullRequestUnknown", err)
	}
}

func TestOpenRequiresAnEffectID(t *testing.T) {
	service := NewPRService(newFakeGitHub(), newMemoryPRStore(), NewGate(nil))
	if _, err := service.Open(context.Background(), CreatePullRequest{RunID: "run_noeffect", Owner: "acme", Repo: "app", Head: "agent/a", Base: "main"}, PullRequestBody{Summary: "x"}); err == nil {
		t.Fatal("a pull request without an effect id must be refused, or a retry can duplicate it")
	}
}

package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/gitops"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
)

// fakePRGitHub is the faithful GitHub stand-in used by the PR handler tests.
type fakePRGitHub struct {
	created      []gitops.PullRequest
	merged       map[int64]string
	deletedRefs  []string
	checks       []gitops.RemoteCheck
	reviews      []gitops.Review
	protected    bool
	required     int
	requiredList []string
	createdCalls int
	mergedCalls  int
}

func newFakePRGitHub() *fakePRGitHub {
	return &fakePRGitHub{
		merged:       map[int64]string{},
		protected:    true,
		required:     1,
		requiredList: []string{"verify"},
	}
}

func (f *fakePRGitHub) CreatePullRequest(_ context.Context, req gitops.CreatePullRequest) (gitops.PullRequest, error) {
	f.createdCalls++
	number := int64(100 + f.createdCalls)
	pr := gitops.PullRequest{
		ID:          "pr_fixture",
		RunID:       req.RunID,
		Number:      number,
		URL:         "https://github.com/acme/app/pull/" + itoa64(number),
		RepoOwner:   req.Owner,
		RepoName:    req.Repo,
		HeadBranch:  req.Head,
		BaseBranch:  req.Base,
		Title:       req.Title,
		IssueNumber: req.Issue,
		HeadSHA:     "head-sha",
		State:       gitops.PROpen,
		EffectID:    req.EffectID,
		Checks:      f.checks,
		Reviews:     f.reviews,
	}
	f.created = append(f.created, pr)
	return pr, nil
}

func (f *fakePRGitHub) GetPullRequest(_ context.Context, _, _ string, number int64) (gitops.PullRequest, error) {
	pr := gitops.PullRequest{Number: number, State: gitops.PROpen, HeadSHA: "head-sha", MergeState: "clean"}
	if sha, ok := f.merged[number]; ok {
		pr.State = gitops.PRMerged
		pr.MergeSHA = sha
		pr.MergeState = "merged"
	}
	return pr, nil
}

func (f *fakePRGitHub) ListReviews(_ context.Context, _, _ string, _ int64) ([]gitops.Review, error) {
	return f.reviews, nil
}

func (f *fakePRGitHub) ListChecks(_ context.Context, _, _, _ string) ([]gitops.RemoteCheck, error) {
	return f.checks, nil
}

func (f *fakePRGitHub) MergePullRequest(_ context.Context, _, _ string, number int64, _ string) (string, error) {
	f.mergedCalls++
	f.merged[number] = "merge-sha"
	return f.merged[number], nil
}

func (f *fakePRGitHub) DeleteRef(_ context.Context, _, _, branch string) error {
	f.deletedRefs = append(f.deletedRefs, branch)
	return nil
}

func (f *fakePRGitHub) BranchProtected(_ context.Context, _, _, _ string) (bool, int, []string, error) {
	if !f.protected {
		return false, 0, nil, nil
	}
	return true, f.required, f.requiredList, nil
}

func itoa64(value int64) string {
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

// fakePRStores is the in-process store used by the handler tests.
type fakePRStores struct {
	prs    map[string][]gitops.PullRequest
	merges map[string][]gitops.MergeAction
}

func newFakePRStores() *fakePRStores {
	return &fakePRStores{prs: map[string][]gitops.PullRequest{}, merges: map[string][]gitops.MergeAction{}}
}

func (f *fakePRStores) SavePullRequest(_ context.Context, pr gitops.PullRequest) error {
	for i, existing := range f.prs[pr.RunID] {
		if existing.EffectID == pr.EffectID {
			f.prs[pr.RunID][i] = pr
			return nil
		}
	}
	f.prs[pr.RunID] = append(f.prs[pr.RunID], pr)
	return nil
}

func (f *fakePRStores) GetPullRequest(_ context.Context, runID string) (gitops.PullRequest, bool, error) {
	list := f.prs[runID]
	if len(list) == 0 {
		return gitops.PullRequest{}, false, nil
	}
	return list[len(list)-1], true, nil
}

func (f *fakePRStores) ListPullRequests(_ context.Context, _, runID string, _ int) ([]gitops.PullRequest, error) {
	return f.prs[runID], nil
}

func (f *fakePRStores) SaveCommit(_ context.Context, _, _, _ string, _ gitops.Commit) error {
	return nil
}

func (f *fakePRStores) RecordMerge(_ context.Context, action gitops.MergeAction) error {
	for _, existing := range f.merges[action.RunID] {
		if existing.EffectID == action.EffectID {
			return nil
		}
	}
	f.merges[action.RunID] = append(f.merges[action.RunID], action)
	return nil
}

func (f *fakePRStores) MergeActions(_ context.Context, runID string, _ int) ([]gitops.MergeAction, error) {
	return f.merges[runID], nil
}

var _ gitops.PullRequestStore = (*fakePRStores)(nil)

// newPRHarness starts a server wired to a real workspace, the fake GitHub
// transport and the durable gitops engine.
func newPRHarness(t *testing.T, client gitops.GitHubClient, stores gitops.PullRequestStore, workspace string) m02Harness {
	t.Helper()
	base := newM02Harness(t, config.EnvTest)
	base.identities.byEmail["owner@example.test"] = "hashed:" + testPassword
	base.runs = newFakeRunsStore()
	base.queue = &fakeTaskQueue{}

	service := NewPullRequestService(client, stores, func(string) string { return workspace })
	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          base.harnessServices(),
		Auth:              apiAuthServices(base, &fakeOrgs{}),
		Encryptor:         base.encryptor,
		Runs:              base.runs,
		Queue:             base.queue,
		PRs:               service,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	return base
}

// prWorkspace returns a real temporary git repository with one commit for the
// M13 pull-request handlers.
func prWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "# fixture\n")
	runGit(t, dir, "init", "--quiet", "--initial-branch=main")
	runGit(t, dir, "config", "user.name", "Fixture")
	runGit(t, dir, "config", "user.email", "fixture@example.test")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	runGit(t, dir, "add", "README.md")
	runGit(t, dir, "commit", "--quiet", "-m", "fixture")
	write("internal/api/server.go", "package api\n")
	write("internal/api/server_test.go", "package api\n")
	return dir
}

// greenLocalChecks reports every required profile as green, so a push gate
// refusal in these tests comes from the field under test and not from a profile
// the fixture forgot to record.
func greenLocalChecks() string {
	profiles := gitops.RequiredProfiles()
	parts := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		parts = append(parts, `{"profile":"`+profile+`","verdict":"green","ran":true}`)
	}
	return `[` + strings.Join(parts, ",") + `]`
}

func openPayload(diffFile string) string {
	return `{
      "workspace_id": "wsp_pr",
      "effect_id": "effect_open_1",
      "head": "agent/run-pr",
      "base": "main",
      "title": "m13: bound the pagination limit",
      "issue_number": 42,
      "repo_owner": "acme",
      "repo_name": "app",
      "summary": "Bound the limit before the offset.",
      "risks": ["The runner is not exercised in CI."],
      "secret_scan_clean": true,
      "pinned_sha": "aaa",
      "base_sha": "aaa",
      "files": ["internal/api/server.go"],
      "checks": ` + greenLocalChecks() + `,
      "commits": [{"message": "m13: bound the limit", "paths": ["internal/api/server.go"]}]
    }`
}

func TestOpenPullRequestRefusesWhenARequiredCheckDidNotRun(t *testing.T) {
	client := newFakePRGitHub()
	workspace := prWorkspace(t)
	harness := newPRHarness(t, client, newFakePRStores(), workspace)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)

	payload := strings.Replace(openPayload(""), greenLocalChecks(), `[{"profile":"go.test","verdict":"green","ran":true},{"profile":"go.build","verdict":"red","ran":false}]`, 1)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/pr", payload,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_pr_1"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "did not run") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if client.createdCalls != 0 {
		t.Fatal("no pull request may be opened when a required check did not run")
	}
}

func TestOpenPullRequestRefusesSecretFindingAndDisallowedPath(t *testing.T) {
	client := newFakePRGitHub()
	harness := newPRHarness(t, client, newFakePRStores(), prWorkspace(t))
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)

	payload := strings.Replace(openPayload(""), `"secret_scan_clean": true`, `"secret_scan_clean": false`, 1)
	payload = strings.Replace(payload, `"files": ["internal/api/server.go"]`, `"files": ["internal/api/server.go", ".github/workflows/ci.yml"]`, 1)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/pr", payload,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_pr_2"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "secret scan") || !strings.Contains(body, "not allowlisted") {
		t.Fatalf("body = %s", body)
	}
}

func TestOpenPullRequestComposesRealCommitsAndOpensOnce(t *testing.T) {
	client := newFakePRGitHub()
	stores := newFakePRStores()
	harness := newPRHarness(t, client, stores, prWorkspace(t))
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)

	payload := strings.Replace(openPayload(""), `"commits": [{"message": "m13: bound the limit", "paths": ["internal/api/server.go"]}]`, `"commits": [
      {"message": "m13: bound the limit", "paths": ["internal/api/server.go"]},
      {"message": "m13: add a regression test", "paths": ["internal/api/server_test.go"]}
    ]`, 1)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/pr", payload,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_pr_3"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "https://github.com/acme/app/pull/101") {
		t.Fatalf("body = %s", body)
	}
	if !strings.Contains(body, "Closes #42") {
		t.Fatalf("the PR body must reference its issue: %s", body)
	}
	if client.createdCalls != 1 {
		t.Fatalf("created calls = %d", client.createdCalls)
	}

	// The same create replayed is reported as the existing pull request, not a
	// second one.
	replay := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/pr", payload,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_pr_3"})
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status = %d body = %s", replay.Code, replay.Body.String())
	}
	if client.createdCalls != 1 {
		t.Fatalf("created calls after replay = %d, want 1", client.createdCalls)
	}
}

func TestOpenPullRequestRefusesWhenASpecProducedNoCommit(t *testing.T) {
	client := newFakePRGitHub()
	harness := newPRHarness(t, client, newFakePRStores(), prWorkspace(t))
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)

	payload := strings.Replace(openPayload(""), `{"message": "m13: bound the limit", "paths": ["internal/api/server.go"]}`, `{"message": "m13: change a file that does not exist", "paths": ["internal/api/absent.go"]}`, 1)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/pr", payload,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_pr_4"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "produced no commit") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestOpenPullRequestValidatesItsRequest(t *testing.T) {
	harness := newPRHarness(t, newFakePRGitHub(), newFakePRStores(), prWorkspace(t))
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)

	for _, replacement := range []struct{ from, to string }{
		{`"effect_id": "effect_open_1"`, `"effect_id": ""`},
		{`"head": "agent/run-pr"`, `"head": ""`},
		{`"repo_owner": "acme"`, `"repo_owner": ""`},
		{`"files": ["internal/api/server.go"]`, `"files": []`},
	} {
		payload := strings.Replace(openPayload(""), replacement.from, replacement.to, 1)
		rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/pr", payload,
			map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_pr_val"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("replacement %s -> %s gave status %d body = %s", replacement.from, replacement.to, rec.Code, rec.Body.String())
		}
	}
}

func TestMergeIsRefusedWhileChecksFailAndThenSucceeds(t *testing.T) {
	client := newFakePRGitHub()
	stores := newFakePRStores()
	harness := newPRHarness(t, client, stores, prWorkspace(t))
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)
	harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/pr", openPayload(""),
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_pr_merge"})

	client.checks = []gitops.RemoteCheck{{Name: "verify", Status: "completed", Conclusion: "failure"}}
	client.reviews = []gitops.Review{{Author: "maintainer", State: "approved", CommitID: "head-sha"}}
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/merge",
		`{"authorized_by":"maintainer@example.test","method":"merge"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_merge_1"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "pr_merge_refused") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if client.mergedCalls != 0 {
		t.Fatal("no merge call may be made while a required check fails")
	}

	client.checks = []gitops.RemoteCheck{{Name: "verify", Status: "completed", Conclusion: "success"}}
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/merge",
		`{"authorized_by":"maintainer@example.test","method":"merge"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_merge_2"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"merge_sha":"merge-sha"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if len(client.deletedRefs) != 1 || client.deletedRefs[0] != "agent/run-pr" {
		t.Fatalf("deleted refs = %v", client.deletedRefs)
	}
	if actions, err := stores.MergeActions(context.Background(), runID, 0); err != nil || len(actions) != 1 {
		t.Fatalf("merge actions = %+v err = %v", actions, err)
	}

	// A second merge of the same pull request is reported, never repeated.
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/merge",
		`{"authorized_by":"maintainer@example.test","method":"merge"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_merge_3"})
	if rec.Code != http.StatusOK {
		t.Fatalf("second merge status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"already_merged":true`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if client.mergedCalls != 1 {
		t.Fatalf("merge calls = %d, want exactly one", client.mergedCalls)
	}
}

func TestMergeRequiresAHumanAuthorization(t *testing.T) {
	client := newFakePRGitHub()
	client.checks = []gitops.RemoteCheck{{Name: "verify", Status: "completed", Conclusion: "success"}}
	client.reviews = []gitops.Review{{Author: "maintainer", State: "approved", CommitID: "head-sha"}}
	harness := newPRHarness(t, client, newFakePRStores(), prWorkspace(t))
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)
	harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/pr", openPayload(""),
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_pr_human"})

	for _, authorizer := range []string{`""`, `"step-5-preview"`, `"reposurgeon-bot"`} {
		rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/merge",
			`{"authorized_by":`+authorizer+`,"method":"merge"}`,
			map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_merge_human"})
		if rec.Code != http.StatusConflict && rec.Code != http.StatusBadRequest {
			t.Fatalf("authorizer %s gave status %d body = %s", authorizer, rec.Code, rec.Body.String())
		}
	}
	if client.mergedCalls != 0 {
		t.Fatal("a model or bot identity must never authorize a merge")
	}
}

// TestMergeRequiresThePermission renders the permission gate: the harness
// member holds run.execute but not merge.execute, so the merge endpoint answers
// 403 before any lifecycle work happens.
func TestMergeRequiresThePermission(t *testing.T) {
	harness := newPRHarness(t, newFakePRGitHub(), newFakePRStores(), prWorkspace(t))
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)
	harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/pr", openPayload(""),
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_pr_perm"})

	withoutPermission := newPRHarnessWithoutMerge(t)
	limitedToken, limitedCSRF := withoutPermission.login(t, "viewer@example.test", testPassword)
	rec := withoutPermission.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/merge",
		`{"authorized_by":"maintainer@example.test"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + limitedToken, "X-CSRF-Token": limitedCSRF, "Idempotency-Key": "idk_merge_perm"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s, want 403 without merge.execute", rec.Code, rec.Body.String())
	}
}

func TestAbandonKeepsTheBranch(t *testing.T) {
	client := newFakePRGitHub()
	harness := newPRHarness(t, client, newFakePRStores(), prWorkspace(t))
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)
	harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/pr", openPayload(""),
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_pr_abandon"})

	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/abandon",
		`{"reason":"three attempts produced no test delta"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_abandon"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"state":"abandoned"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if len(client.deletedRefs) != 0 {
		t.Fatalf("an abandoned run must keep its branch: %v", client.deletedRefs)
	}

	// A reason is required so the abandoned change stays explainable.
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/abandon", `{"reason":""}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_abandon_2"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestGetPullRequestReportsNotFoundWhenNoneExists(t *testing.T) {
	harness := newPRHarness(t, newFakePRGitHub(), newFakePRStores(), prWorkspace(t))
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/"+runID+"/pr", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestGetPullRequestReportsTheTrackedPullRequest(t *testing.T) {
	client := newFakePRGitHub()
	client.checks = []gitops.RemoteCheck{{Name: "verify", Status: "in_progress"}}
	harness := newPRHarness(t, client, newFakePRStores(), prWorkspace(t))
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)
	harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/pr", openPayload(""),
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_pr_get"})

	rec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/"+runID+"/pr", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	// The pending check is shown as pending, never as a pass.
	if !strings.Contains(rec.Body.String(), "in_progress") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestPullRequestUnavailableWithoutService(t *testing.T) {
	harness := newPRHarness(t, newFakePRGitHub(), newFakePRStores(), prWorkspace(t))
	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          harness.harnessServices(),
		Auth:              apiAuthServices(harness, &fakeOrgs{}),
		Encryptor:         harness.encryptor,
		Runs:              newFakeRunsStore(),
		Queue:             &fakeTaskQueue{},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	harness.handler = server.Handler()
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/run_x/pr", openPayload(""),
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_pr_none"})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

var _ = errors.Is

// runGit executes git in a test workspace.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...) // #nosec G204 -- git runs in a test-owned directory
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
	}
}

// newPRHarnessWithoutMerge builds a harness whose member lacks merge.execute,
// so the permission gate can be exercised on the merge endpoint.
func newPRHarnessWithoutMerge(t *testing.T) m02Harness {
	t.Helper()
	base := newM02Harness(t, config.EnvTest)
	base.identities.byEmail["viewer@example.test"] = "hashed:" + testPassword
	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          base.harnessServices(),
		Auth:              apiAuthServices(base, &fakeOrgs{}),
		Encryptor:         base.encryptor,
		Runs:              newFakeRunsStore(),
		Queue:             &fakeTaskQueue{},
		PRs:               NewPullRequestService(newFakePRGitHub(), newFakePRStores(), func(string) string { return prWorkspace(t) }),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	return base
}

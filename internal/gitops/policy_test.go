package gitops

import (
	"strings"
	"testing"
)

func TestPushGateRefusesWhenARequiredCheckDidNotRun(t *testing.T) {
	gate := NewGate([]string{"go.test", "go.build"}, "internal/")
	decision := gate.CheckPush(
		[]LocalCheck{
			{Profile: "go.test", Verdict: VerdictGreen, Ran: true},
			{Profile: "go.build", Verdict: VerdictRed, Ran: false, Detail: "not executed"},
		},
		SecretScan{Clean: true},
		[]string{"internal/api/server.go"},
		BaseState{PinnedSHA: "aaa", CurrentSHA: "aaa"},
	)
	if decision.Allowed {
		t.Fatal("a check that did not run must refuse the push")
	}
	if len(decision.Reasons) != 1 || decision.Reasons[0] != "required check go.build did not run" {
		t.Fatalf("reasons = %v", decision.Reasons)
	}
}

func TestPushGateRefusesSecretFindingsAndNonAllowlistedPaths(t *testing.T) {
	gate := NewGate([]string{"go.test"}, "internal/")
	decision := gate.CheckPush(
		[]LocalCheck{{Profile: "go.test", Verdict: VerdictGreen, Ran: true}},
		SecretScan{Clean: false, Findings: []string{"web/src/api/client.ts:12 looks like a token"}},
		[]string{"internal/api/server.go", ".github/workflows/ci.yml", "web/src/App.vue"},
		BaseState{PinnedSHA: "aaa", CurrentSHA: "aaa"},
	)
	if decision.Allowed {
		t.Fatal("secret findings and disallowed paths must refuse the push")
	}
	if len(decision.Reasons) != 3 {
		t.Fatalf("reasons = %v, want the scan plus both paths", decision.Reasons)
	}
}

func TestPushGateRefusesAMovedBaseWithoutRebase(t *testing.T) {
	gate := NewGate([]string{"go.test"}, "internal/")
	decision := gate.CheckPush(
		[]LocalCheck{{Profile: "go.test", Verdict: VerdictGreen, Ran: true}},
		SecretScan{Clean: true},
		[]string{"internal/api/server.go"},
		BaseState{PinnedSHA: "aaa", CurrentSHA: "bbb"},
	)
	if decision.Allowed {
		t.Fatal("a moved base must refuse the push until the change is rebased and retested")
	}
	decision = gate.CheckPush(
		[]LocalCheck{{Profile: "go.test", Verdict: VerdictGreen, Ran: true}},
		SecretScan{Clean: true},
		[]string{"internal/api/server.go"},
		BaseState{PinnedSHA: "aaa", CurrentSHA: "bbb", Rebased: true, Retested: true},
	)
	if !decision.Allowed {
		t.Fatalf("a rebased and retested change must be allowed: %v", decision.Reasons)
	}
}

func TestPushGateAllowsACleanChange(t *testing.T) {
	gate := NewGate([]string{"go.test", "go.build"}, "internal/", "web/src/")
	decision := gate.CheckPush(
		[]LocalCheck{
			{Profile: "go.test", Verdict: VerdictGreen, Ran: true},
			{Profile: "go.build", Verdict: VerdictGreen, Ran: true},
		},
		SecretScan{Clean: true},
		[]string{"internal/api/server.go", "web/src/App.vue"},
		BaseState{PinnedSHA: "aaa", CurrentSHA: "aaa"},
	)
	if !decision.Allowed {
		t.Fatalf("decision = %+v, want allowed", decision)
	}
	if decision.Stage != "push" {
		t.Fatalf("stage = %s", decision.Stage)
	}
}

func TestPushGateDeniesUnprotectedPathsByDefault(t *testing.T) {
	gate := NewGate([]string{"go.test"}, "internal/")
	decision := gate.CheckPush(
		[]LocalCheck{{Profile: "go.test", Verdict: VerdictGreen, Ran: true}},
		SecretScan{Clean: true},
		[]string{"migrations/0016_extra.up.sql", "go.mod", "docs/specification/M14.md", "CLAUDE.md"},
		BaseState{PinnedSHA: "aaa", CurrentSHA: "aaa"},
	)
	if decision.Allowed {
		t.Fatal("governance paths must always refuse an agent-pushed change")
	}
	if len(decision.Reasons) != 4 {
		t.Fatalf("reasons = %v", decision.Reasons)
	}
}

func TestMergeGateRefusesModelIdentitiesAsAuthorizers(t *testing.T) {
	gate := NewGate(nil)
	for _, identity := range []string{"step-5-preview", "step-3.7-flash", "gpt-5", "claude-sonnet", "the model", "assistant", "reposurgeon-bot"} {
		decision := gate.CheckMerge(MergeRequest{
			PullRequestNumber:  11,
			Checks:             []RemoteCheck{{Name: "verify", Status: "completed", Conclusion: "success"}},
			Reviews:            []Review{{Author: "a", State: "approved"}},
			RequiredChecks:     []string{"verify"},
			RequiredApprovals:  1,
			ProtectionActive:   true,
			PolicyPermitsMerge: true,
			AuthorizedBy:       identity,
		})
		if decision.Allowed {
			t.Fatalf("%s must never authorize a merge", identity)
		}
		if len(decision.Reasons) != 1 || !strings.Contains(decision.Reasons[0], "human") {
			t.Fatalf("reasons for %s = %v", identity, decision.Reasons)
		}
	}
	// A human operator with the same shape of identifier is accepted.
	decision := gate.CheckMerge(MergeRequest{
		PullRequestNumber:  11,
		Checks:             []RemoteCheck{{Name: "verify", Status: "completed", Conclusion: "success"}},
		Reviews:            []Review{{Author: "a", State: "approved"}},
		RequiredChecks:     []string{"verify"},
		RequiredApprovals:  1,
		ProtectionActive:   true,
		PolicyPermitsMerge: true,
		AuthorizedBy:       "maintainer@example.test",
	})
	if !decision.Allowed {
		t.Fatalf("a human authorizer must be accepted: %v", decision.Reasons)
	}
}

func TestMergeGateRefusesWithoutHumanAuthorization(t *testing.T) {
	gate := NewGate(nil)
	checks := []RemoteCheck{{Name: "verify", Status: "completed", Conclusion: "success"}}
	reviews := []Review{{Author: "maintainer", State: "approved", CommitID: "sha"}}
	request := MergeRequest{
		PullRequestNumber:  7,
		Checks:             checks,
		Reviews:            reviews,
		RequiredChecks:     []string{"verify"},
		RequiredApprovals:  1,
		ProtectionActive:   true,
		PolicyPermitsMerge: true,
	}
	decision := gate.CheckMerge(request)
	if decision.Allowed {
		t.Fatal("a model claim must never authorize a merge")
	}
	if len(decision.Reasons) != 1 || decision.Reasons[0] == "" {
		t.Fatalf("reasons = %v", decision.Reasons)
	}
	request.AuthorizedBy = "maintainer@example.test"
	if decision = gate.CheckMerge(request); !decision.Allowed {
		t.Fatalf("an authorized merge with green checks must be allowed: %v", decision.Reasons)
	}
}

func TestMergeGateRefusesPendingFailedAndMissingChecks(t *testing.T) {
	gate := NewGate(nil)
	request := MergeRequest{
		PullRequestNumber:  8,
		Checks:             []RemoteCheck{{Name: "verify", Status: "in_progress"}},
		Reviews:            []Review{{Author: "m", State: "approved"}},
		RequiredChecks:     []string{"verify", "secret scan"},
		RequiredApprovals:  1,
		ProtectionActive:   true,
		PolicyPermitsMerge: true,
		AuthorizedBy:       "maintainer@example.test",
	}
	decision := gate.CheckMerge(request)
	if decision.Allowed {
		t.Fatal("an unfinished check must block the merge")
	}
	if len(decision.Reasons) != 2 {
		t.Fatalf("reasons = %v, want the pending and the unreported check", decision.Reasons)
	}

	request.Checks = []RemoteCheck{{Name: "verify", Status: "completed", Conclusion: "failure"}}
	decision = gate.CheckMerge(request)
	if decision.Allowed || len(decision.Reasons) != 2 {
		t.Fatalf("a failed check must block the merge: %+v", decision)
	}

	request.Checks = []RemoteCheck{
		{Name: "verify", Status: "completed", Conclusion: "success"},
		{Name: "secret scan", Status: "completed", Conclusion: "skipped"},
	}
	decision = gate.CheckMerge(request)
	if !decision.Allowed {
		t.Fatalf("a skipped required check is not a failure: %v", decision.Reasons)
	}
}

func TestMergeGateRefusesWithoutRequiredApprovals(t *testing.T) {
	gate := NewGate(nil)
	decision := gate.CheckMerge(MergeRequest{
		PullRequestNumber:  9,
		Checks:             []RemoteCheck{{Name: "verify", Status: "completed", Conclusion: "success"}},
		Reviews:            []Review{{Author: "a", State: "commented"}, {Author: "b", State: "changes_requested"}},
		RequiredChecks:     []string{"verify"},
		RequiredApprovals:  1,
		ProtectionActive:   true,
		PolicyPermitsMerge: true,
		AuthorizedBy:       "maintainer@example.test",
	})
	if decision.Allowed {
		t.Fatal("comments and change requests are not approvals")
	}
	if len(decision.Reasons) != 1 {
		t.Fatalf("reasons = %v", decision.Reasons)
	}
}

func TestMergeGateRefusesWhenRepositoryPolicyForbidsMerging(t *testing.T) {
	gate := NewGate(nil)
	decision := gate.CheckMerge(MergeRequest{
		PullRequestNumber:  10,
		Checks:             []RemoteCheck{{Name: "verify", Status: "completed", Conclusion: "success"}},
		Reviews:            []Review{{Author: "a", State: "approved"}},
		RequiredChecks:     []string{"verify"},
		RequiredApprovals:  1,
		PolicyPermitsMerge: false,
		AuthorizedBy:       "maintainer@example.test",
	})
	if decision.Allowed {
		t.Fatal("the repository policy must be able to refuse a merge on its own")
	}
}

func TestMergeGateRefusesWhenNoPullRequestIsOpen(t *testing.T) {
	gate := NewGate(nil)
	decision := gate.CheckMerge(MergeRequest{AuthorizedBy: "maintainer@example.test", PolicyPermitsMerge: true})
	if decision.Allowed {
		t.Fatal("merging without a pull request must be refused")
	}
	found := false
	for _, reason := range decision.Reasons {
		if reason == "no pull request is open for this branch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reasons = %v", decision.Reasons)
	}
}

func TestPullRequestBodyRendersEvidenceNotClaims(t *testing.T) {
	body := PullRequestBody{
		Summary: "Bound the pagination limit.",
		TestEvidence: []LocalCheck{
			{Profile: "go.test", Verdict: VerdictGreen, Ran: true},
			{Profile: "npm.test", Verdict: VerdictRed, Ran: true, Detail: "2 failures"},
			{Profile: "go.build", Ran: false},
		},
		Risks:       []string{"The runner is not exercised in CI."},
		Files:       []string{"internal/api/list.go"},
		IssueNumber: 42,
	}.Render()

	for _, want := range []string{
		"## Summary",
		"Bound the pagination limit.",
		"- `go.test`: green",
		"- `npm.test`: red — 2 failures",
		"- `go.build`: did not run",
		"- internal/api/list.go",
		"- The runner is not exercised in CI.",
		"Closes #42",
		"Generated by RepoSurgeon.",
	} {
		if !contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
	// A check that did not run is never rendered as a pass.
	if contains(body, "go.build: green") {
		t.Fatal("an unrun check must never read as green")
	}
}

func TestStatusDisplayKeepsGitHubWords(t *testing.T) {
	cases := map[string]string{
		"success":      "success",
		"failure":      "failed",
		"in_progress":  "pending",
		"queued":       "pending",
		"":             "not reported",
		"weird_status": "weird_status",
	}
	for input, want := range cases {
		if got := StatusDisplay(input); got != want {
			t.Fatalf("StatusDisplay(%q) = %q, want %q", input, got, want)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

package gitops

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// LocalCheck is one locally executed gate result: a check profile that ran
// before anything is pushed. A check that did not run is a failure, never a
// pass by omission.
type LocalCheck struct {
	// Profile is the command profile name, for example go.test.
	Profile string `json:"profile"`
	// Verdict is green or red.
	Verdict string `json:"verdict"`
	// Ran reports whether the profile actually executed.
	Ran bool `json:"ran"`
	// Detail is the operator-facing summary.
	Detail string `json:"detail,omitempty"`
}

// SecretScan is the pre-push secret scan result.
type SecretScan struct {
	// Clean reports that no finding was raised.
	Clean bool `json:"clean"`
	// Findings describes what was found.
	Findings []string `json:"findings,omitempty"`
}

// BaseState records what the branch is built on.
type BaseState struct {
	// PinnedSHA is the commit the run was created from.
	PinnedSHA string `json:"pinned_sha"`
	// CurrentSHA is what the base branch points at now.
	CurrentSHA string `json:"current_sha"`
	// Rebased reports that the caller rebased and retested.
	Rebased bool `json:"rebased"`
	// Retested reports that the required checks ran after the rebase.
	Retested bool `json:"retested"`
}

// AllowlistConfig bounds which paths a change may touch.
type AllowlistConfig struct {
	// Allowed is the set of permitted path prefixes; empty means everything is
	// forbidden, because an unset allowlist must not mean "anything goes".
	Allowed []string `json:"allowed"`
	// Denied is the set of always-forbidden path prefixes.
	Denied []string `json:"denied"`
}

// Decision is the gate's verdict with its reasons. A refusal always carries
// every rule that blocked it so an operator sees the whole picture.
type Decision struct {
	// Allowed reports whether the action may proceed.
	Allowed bool `json:"allowed"`
	// Reasons lists every rule that refused the action.
	Reasons []string `json:"reasons"`
	// Stage names the gate, for example push or merge.
	Stage string `json:"stage"`
}

// Gate decides whether pushing and merging are permitted. It is pure: every
// input is supplied by the caller and the answer is derived from the inputs
// alone, so no model or provider can talk it into allowing something.
type Gate struct {
	// RequiredProfiles are the checks that must have run green.
	RequiredProfiles []string
	// Allowlist bounds the changed files.
	Allowlist AllowlistConfig
}

// NewGate builds the gate of record.
func NewGate(required []string, allowed ...string) *Gate {
	return &Gate{
		RequiredProfiles: required,
		Allowlist:        AllowlistConfig{Allowed: allowed, Denied: DefaultProtectedPaths()},
	}
}

// CheckPush evaluates everything a push requires: green required checks, a
// clean secret scan, an allowlisted changed-file set, and a base that has not
// moved since the run was pinned unless the change was rebased and retested.
func (g *Gate) CheckPush(checks []LocalCheck, scan SecretScan, files []string, base BaseState) Decision {
	decision := Decision{Stage: "push", Reasons: []string{}}
	byProfile := map[string]LocalCheck{}
	for _, check := range checks {
		byProfile[check.Profile] = check
	}
	for _, profile := range g.RequiredProfiles {
		check, ok := byProfile[profile]
		if !ok || !check.Ran {
			decision.Reasons = append(decision.Reasons, fmt.Sprintf("required check %s did not run", profile))
			continue
		}
		if check.Verdict != VerdictGreen {
			decision.Reasons = append(decision.Reasons, fmt.Sprintf("required check %s is %s", profile, check.Verdict))
		}
	}
	if !scan.Clean {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf("secret scan reported %d finding(s)", len(scan.Findings)))
	}
	denied := g.Allowlist.Violations(files)
	for _, path := range denied {
		decision.Reasons = append(decision.Reasons, "changed path is not allowlisted: "+path)
	}
	if base.PinnedSHA != "" && base.CurrentSHA != "" && base.PinnedSHA != base.CurrentSHA && (!base.Rebased || !base.Retested) {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf("base moved from %s to %s and the change was not rebased and retested", short(base.PinnedSHA), short(base.CurrentSHA)))
	}
	decision.Allowed = len(decision.Reasons) == 0
	return decision
}

// MergeRequest carries the state a merge gate evaluates: the pull request,
// its real GitHub checks and reviews, the protection requirements and the human
// who authorized the merge.
type MergeRequest struct {
	// PullRequestNumber is the GitHub PR number.
	PullRequestNumber int64
	// Checks are the real GitHub check runs / statuses.
	Checks []RemoteCheck
	// Reviews are the real GitHub reviews.
	Reviews []Review
	// RequiredChecks are the branch-protection required check names.
	RequiredChecks []string
	// RequiredApprovals is the branch-protection approval count.
	RequiredApprovals int
	// ProtectionActive reports whether branch protection applies.
	ProtectionActive bool
	// AuthorizedBy is the human who authorized the merge; empty means nobody.
	AuthorizedBy string
	// PolicyPermitsMerge reports whether the repository policy permits merging
	// this branch.
	PolicyPermitsMerge bool
}

// RemoteCheck is one GitHub check run or status for a head SHA.
type RemoteCheck struct {
	// Name is the check name reported by GitHub.
	Name string `json:"name"`
	// Status is queued, in_progress or completed.
	Status string `json:"status"`
	// Conclusion is success, failure, canceled or skipped.
	Conclusion string `json:"conclusion,omitempty"`
	// DetailsURL links to the provider's run.
	DetailsURL string `json:"details_url,omitempty"`
}

// Review is one GitHub review.
type Review struct {
	// Author is the reviewing user.
	Author string `json:"author"`
	// State is approved, changes_requested or commented.
	State string `json:"state"`
	// CommitID is the head SHA the review was left on.
	CommitID string `json:"commit_id"`
}

// CheckMerge answers whether the pull request may be merged right now.
func (g *Gate) CheckMerge(request MergeRequest) Decision {
	decision := Decision{Stage: "merge", Reasons: []string{}}
	if request.PullRequestNumber <= 0 {
		decision.Reasons = append(decision.Reasons, "no pull request is open for this branch")
	}
	if !request.PolicyPermitsMerge {
		decision.Reasons = append(decision.Reasons, "repository policy does not permit merging this branch")
	}
	if refusal := authorizedByRefusal(request.AuthorizedBy); refusal != "" {
		decision.Reasons = append(decision.Reasons, refusal)
	}
	checksByName := map[string]RemoteCheck{}
	for _, check := range request.Checks {
		checksByName[check.Name] = check
	}
	for _, required := range request.RequiredChecks {
		check, ok := checksByName[required]
		if !ok {
			decision.Reasons = append(decision.Reasons, "required GitHub check "+required+" has not reported")
			continue
		}
		switch {
		case check.Status != "completed":
			decision.Reasons = append(decision.Reasons, "required GitHub check "+required+" is still "+StatusDisplay(check.Status))
		case check.Conclusion != "success" && check.Conclusion != "skipped" && check.Conclusion != "neutral":
			decision.Reasons = append(decision.Reasons, "required GitHub check "+required+" concluded "+StatusDisplay(check.Conclusion))
		}
	}
	approvals := 0
	for _, review := range request.Reviews {
		if review.State == "approved" {
			approvals++
		}
	}
	if approvals < request.RequiredApprovals {
		decision.Reasons = append(decision.Reasons, fmt.Sprintf("%d of %d required approval(s) recorded", approvals, request.RequiredApprovals))
	}
	decision.Allowed = len(decision.Reasons) == 0
	return decision
}

// Verdict constants mirror the check verdicts so push and merge gates agree.
const (
	VerdictGreen = "green"
	VerdictRed   = "red"
)

// StatusDisplay renders a GitHub status or conclusion in operator language
// without changing its meaning.
func StatusDisplay(value string) string {
	switch value {
	case "":
		return "not reported"
	case "success":
		return "success"
	case "failure":
		return "failed"
	case "canceled":
		return "canceled"
	case "skipped":
		return "skipped"
	case "neutral":
		return "neutral"
	case "queued", "in_progress":
		return "pending"
	}
	return value
}

// Verdict constants gate results keep in sync with checks.
var (
	// ErrGateRefused is the sentinel a refusal wraps.
	ErrGateRefused = errors.New("gitops: gate refused the action")
)

// Violations returns the changed paths that are not allowed.
func (a AllowlistConfig) Violations(files []string) []string {
	var out []string
	for _, file := range files {
		normalized := filepath.ToSlash(strings.TrimSpace(file))
		if normalized == "" {
			continue
		}
		if matchesAny(normalized, a.Denied) || !matchesAny(normalized, a.Allowed) {
			out = append(out, normalized)
		}
	}
	return out
}

func matchesAny(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		normalized := filepath.ToSlash(strings.TrimSpace(prefix))
		if normalized == "" {
			continue
		}
		if path == normalized || strings.HasPrefix(path, normalized) {
			return true
		}
	}
	return false
}

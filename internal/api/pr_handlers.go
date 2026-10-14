package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/gitops"
)

// OpenPullRequestRequest is the accepted payload for opening a pull request.
type OpenPullRequestRequest struct {
	// WorkspaceID is the workspace the branch lives in.
	WorkspaceID string `json:"workspace_id"`
	// EffectID makes the create exactly-once across webhooks and retries.
	EffectID string `json:"effect_id"`
	// Head and Base are the branches.
	Head string `json:"head"`
	Base string `json:"base"`
	// Title is the pull-request title.
	Title string `json:"title"`
	// IssueNumber links the issue the change resolves.
	IssueNumber int64 `json:"issue_number"`
	// Summary is the structured PR body summary.
	Summary string `json:"summary"`
	// Risks lists the known risks recorded with the change.
	Risks []string `json:"risks"`
	// Commits are the per-logical-change commits to compose before pushing.
	Commits []CommitSpecRequest `json:"commits"`
	// Checks are the local check results recorded before the push.
	Checks []LocalCheckRequest `json:"checks"`
	// Files are the changed files the push must be allowlisted against.
	Files []string `json:"files"`
	// RepoOwner and RepoName identify the repository.
	RepoOwner string `json:"repo_owner"`
	RepoName  string `json:"repo_name"`
	// SecretScanClean reports the pre-push secret scan result.
	SecretScanClean bool `json:"secret_scan_clean"`
	// PinnedSHA and BaseSHA describe the base the branch was built on.
	PinnedSHA string `json:"pinned_sha"`
	BaseSHA   string `json:"base_sha"`
}

// CommitSpecRequest is one logical commit the operator asked for.
type CommitSpecRequest struct {
	Message string   `json:"message"`
	Paths   []string `json:"paths"`
}

// LocalCheckRequest is one locally executed check result.
type LocalCheckRequest struct {
	Profile string `json:"profile"`
	Verdict string `json:"verdict"`
	Ran     bool   `json:"ran"`
	Detail  string `json:"detail"`
}

// MergePullRequest is the accepted payload for merging a pull request.
type MergePullRequest struct {
	// AuthorizedBy is the human who authorizes the merge; a model identity is
	// rejected by the gate.
	AuthorizedBy string `json:"authorized_by"`
	// Method is the GitHub merge method, for example merge or squash.
	Method string `json:"method"`
	// EffectID makes the merge exactly-once.
	EffectID string `json:"effect_id"`
}

// AbandonRequest is the accepted payload for abandoning a pull request.
type AbandonRequest struct {
	// Reason explains why the change is being abandoned.
	Reason string `json:"reason"`
}

// handleOpenPullRequest composes the branch, pushes it and opens one pull
// request. Every step is gated, so a failing local check, a secret finding, a
// disallowed path or a moved base refuses the push before GitHub is called.
func (s *Server) handleOpenPullRequest(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "run.execute") {
		return
	}
	if s.prs == nil {
		writePRUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	var payload OpenPullRequestRequest
	if !decodeInto(w, r, &payload) {
		return
	}
	if err := validateOpenRequest(payload); err != nil {
		writeDomainError(w, r, err)
		return
	}

	// A create that already recorded a pull request for this effect is a
	// replay: answer with the recorded one instead of re-composing the branch,
	// which would now have nothing to commit.
	if existing, found, err := s.prs.FindPullRequest(r.Context(), session.OrganizationID, runID, payload.EffectID); err != nil {
		writeDomainError(w, r, err)
		return
	} else if found {
		WriteJSON(w, http.StatusOK, pullRequestView(existing, nil, run.ID))
		return
	}

	// The push gate runs before any commit is composed or pushed.
	decision := s.prs.Engine.Gate.CheckPush(toLocalChecks(payload.Checks), gitops.SecretScan{Clean: payload.SecretScanClean, Findings: nil}, payload.Files, gitops.BaseState{PinnedSHA: payload.PinnedSHA, CurrentSHA: payload.BaseSHA})
	if !decision.Allowed {
		WriteError(w, r, &Error{
			Status:  statusConflictFor(decision),
			Code:    "pr_push_refused",
			Message: strings.Join(decision.Reasons, "; "),
		}, map[string]any{"reasons": decision.Reasons, "stage": decision.Stage})
		return
	}

	commits, skipped, err := s.prs.ComposeCommits(r.Context(), payload.WorkspaceID, toCommitSpecs(payload.Commits))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if len(skipped) > 0 {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: prSkipMessage(skipped),
		}, nil)
		return
	}
	if len(commits) == 0 {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "no logical change produced a commit",
		}, nil)
		return
	}

	body := gitops.PullRequestBody{
		Summary:      payload.Summary,
		TestEvidence: toLocalChecks(payload.Checks),
		Risks:        payload.Risks,
		Files:        payload.Files,
		IssueNumber:  payload.IssueNumber,
	}
	opened, err := s.prs.Engine.Open(r.Context(), gitops.CreatePullRequest{
		RunID:       runID,
		WorkspaceID: payload.WorkspaceID,
		Owner:       payload.RepoOwner,
		Repo:        payload.RepoName,
		Head:        payload.Head,
		Base:        firstNonEmptyValue(payload.Base, "main"),
		Title:       payload.Title,
		Issue:       payload.IssueNumber,
		EffectID:    payload.EffectID,
	}, body)
	if err != nil {
		if errors.Is(err, gitops.ErrPullRequestExists) {
			WriteJSON(w, http.StatusOK, pullRequestView(opened, nil, run.ID))
			return
		}
		writeDomainError(w, r, err)
		return
	}
	for _, commit := range commits {
		if err := s.prs.Store.SaveCommit(r.Context(), opened.ID, runID, session.OrganizationID, commit); err != nil {
			writeDomainError(w, r, err)
			return
		}
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      run.ProjectID,
		Action:         "pr.opened",
		EntityType:     "pull_request",
		EntityID:       opened.ID,
		Payload: map[string]any{
			"run_id":       runID,
			"pull_request": opened.Number,
			"url":          opened.URL,
			"commits":      len(commits),
			"effect_id":    payload.EffectID,
		},
	})
	WriteJSON(w, http.StatusCreated, pullRequestView(opened, commits, run.ID))
}

// handleGetPullRequest refreshes and returns the tracked pull request.
func (s *Server) handleGetPullRequest(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.prs == nil {
		writePRUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	pr, ok, err := s.prs.Store.GetPullRequest(r.Context(), runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if !ok {
		WriteError(w, r, ErrNotFound, map[string]any{"run_id": runID})
		return
	}
	// Refresh from GitHub when the pull request exists so the check and review
	// state an operator reads is the real one.
	current, refreshErr := s.prs.Engine.Status(r.Context(), runID)
	if refreshErr == nil {
		pr = current
	}
	commits, commitErr := s.prs.Store.ListPullRequests(r.Context(), session.OrganizationID, runID, 0)
	_ = commits
	_ = commitErr
	WriteJSON(w, http.StatusOK, pullRequestView(pr, nil, run.ID))
}

// handleMergePullRequest merges a pull request only when the gate, the real
// GitHub checks and an explicit human authorization all permit it.
func (s *Server) handleMergePullRequest(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "merge.execute") {
		return
	}
	if s.prs == nil {
		writePRUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	var payload MergePullRequest
	if !decodeInto(w, r, &payload) {
		return
	}
	if strings.TrimSpace(payload.AuthorizedBy) == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "authorized_by is required; a model's own claim never authorizes a merge",
		}, nil)
		return
	}
	merged, action, decision, err := s.prs.Engine.Merge(r.Context(), runID, payload.AuthorizedBy, payload.Method)
	if err != nil {
		if errors.Is(err, gitops.ErrPullRequestUnknown) {
			WriteError(w, r, ErrNotFound, map[string]any{"run_id": runID})
			return
		}
		if errors.Is(err, gitops.ErrGateRefused) {
			WriteError(w, r, &Error{
				Status:  statusConflictFor(decision),
				Code:    "pr_merge_refused",
				Message: strings.Join(decision.Reasons, "; "),
			}, map[string]any{"reasons": decision.Reasons, "stage": decision.Stage})
			return
		}
		if errors.Is(err, gitops.ErrAlreadyMerged) {
			WriteJSON(w, http.StatusOK, map[string]any{
				"run_id":         run.ID,
				"pull_request":   merged,
				"merge_action":   action,
				"merge_sha":      merged.MergeSHA,
				"already_merged": true,
			})
			return
		}
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      run.ProjectID,
		Action:         "pr.merged",
		EntityType:     "pull_request",
		EntityID:       merged.ID,
		Payload: map[string]any{
			"run_id":         runID,
			"pull_request":   merged.Number,
			"merge_sha":      merged.MergeSHA,
			"authorized_by":  payload.AuthorizedBy,
			"branch_deleted": merged.BranchDeleted,
			"action_id":      action.ID,
		},
	})
	WriteJSON(w, http.StatusOK, map[string]any{
		"run_id":         run.ID,
		"pull_request":   merged,
		"merge_action":   action,
		"merge_sha":      merged.MergeSHA,
		"already_merged": false,
		"decision":       decision,
	})
}

// handleAbandonPullRequest closes a pull request without merging and keeps the
// branch and its evidence.
func (s *Server) handleAbandonPullRequest(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "run.execute") {
		return
	}
	if s.prs == nil {
		writePRUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	run, err := s.runs.Get(r.Context(), session.OrganizationID, runID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	var payload AbandonRequest
	if !decodeInto(w, r, &payload) {
		return
	}
	if strings.TrimSpace(payload.Reason) == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "a reason is required so the abandoned change stays explainable",
		}, nil)
		return
	}
	abandoned, err := s.prs.Engine.Abandon(r.Context(), runID, strings.TrimSpace(payload.Reason))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      run.ProjectID,
		Action:         "pr.abandoned",
		EntityType:     "pull_request",
		EntityID:       abandoned.ID,
		Payload: map[string]any{
			"run_id":       runID,
			"pull_request": abandoned.Number,
			"reason":       abandoned.BlockedReason,
			"state":        abandoned.State,
		},
	})
	WriteJSON(w, http.StatusOK, map[string]any{
		"run_id":       run.ID,
		"pull_request": abandoned,
		"abandoned":    true,
	})
}

// pullRequestView renders the tracked pull request for the UI.
func pullRequestView(pr gitops.PullRequest, commits []gitops.Commit, _ string) map[string]any {
	return map[string]any{
		"id":             pr.ID,
		"run_id":         pr.RunID,
		"number":         pr.Number,
		"url":            pr.URL,
		"repo_owner":     pr.RepoOwner,
		"repo_name":      pr.RepoName,
		"head_branch":    pr.HeadBranch,
		"base_branch":    pr.BaseBranch,
		"title":          pr.Title,
		"body":           pr.Body,
		"issue_number":   pr.IssueNumber,
		"state":          pr.State,
		"checks":         pr.Checks,
		"reviews":        pr.Reviews,
		"approval_count": pr.ApprovalCount,
		"merge_state":    pr.MergeState,
		"blocked_reason": pr.BlockedReason,
		"merge_sha":      pr.MergeSHA,
		"branch_deleted": pr.BranchDeleted,
		"commits":        commits,
		"created_at":     pr.CreatedAt,
		"updated_at":     pr.UpdatedAt,
	}
}

var (
	errPRWorkspaceUnknown = errPRService("the workspace could not be resolved")
)

type errPRService string

func (e errPRService) Error() string { return string(e) }

func toCommitSpecs(specs []CommitSpecRequest) []gitops.CommitSpec {
	out := make([]gitops.CommitSpec, 0, len(specs))
	for _, spec := range specs {
		out = append(out, gitops.CommitSpec{Message: spec.Message, Paths: spec.Paths})
	}
	return out
}

func toLocalChecks(checks []LocalCheckRequest) []gitops.LocalCheck {
	out := make([]gitops.LocalCheck, 0, len(checks))
	for _, check := range checks {
		out = append(out, gitops.LocalCheck{
			Profile: check.Profile,
			Verdict: check.Verdict,
			Ran:     check.Ran,
			Detail:  check.Detail,
		})
	}
	return out
}

func validateOpenRequest(payload OpenPullRequestRequest) error {
	switch {
	case strings.TrimSpace(payload.WorkspaceID) == "":
		return &checkError{message: "workspace_id is required"}
	case strings.TrimSpace(payload.EffectID) == "":
		return &checkError{message: "effect_id is required, or a retry could open a second pull request"}
	case strings.TrimSpace(payload.Head) == "":
		return &checkError{message: "head is required"}
	case strings.TrimSpace(payload.Title) == "":
		return &checkError{message: "title is required"}
	case strings.TrimSpace(payload.RepoOwner) == "" || strings.TrimSpace(payload.RepoName) == "":
		return &checkError{message: "repo_owner and repo_name are required"}
	case len(payload.Commits) == 0:
		return &checkError{message: "at least one logical change is required"}
	case len(payload.Files) == 0:
		return &checkError{message: "the changed file list is required so the allowlist can be enforced"}
	}
	for _, commit := range payload.Commits {
		if strings.TrimSpace(commit.Message) == "" || len(commit.Paths) == 0 {
			return &checkError{message: "every logical change needs a message and at least one path"}
		}
	}
	return nil
}

// prSkipMessage names the logical changes that produced no commit.
func prSkipMessage(skipped []gitops.CommitSpec) string {
	messages := make([]string, 0, len(skipped))
	for _, spec := range skipped {
		subject := strings.SplitN(spec.Message, "\n", 2)[0]
		if trimmed := strings.TrimSpace(subject); trimmed != "" {
			messages = append(messages, trimmed)
		}
	}
	return "these logical changes produced no commit: " + strings.Join(messages, "; ")
}

// statusConflictFor maps a refusal onto its HTTP status. A push gate refusal is
// a validation failure of the request; a merge refusal is a conflict with the
// recorded evidence.
func statusConflictFor(decision gitops.Decision) int {
	if decision.Stage == "merge" {
		return http.StatusConflict
	}
	return http.StatusBadRequest
}

// writePRUnavailable marks the pull-request service as unconfigured.
func writePRUnavailable(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, &Error{
		Status:  http.StatusServiceUnavailable,
		Code:    "pr_unavailable",
		Message: "the pull request service is not configured on this server",
	}, nil)
}

// firstNonEmptyValue returns the first non-empty value, or empty.
func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

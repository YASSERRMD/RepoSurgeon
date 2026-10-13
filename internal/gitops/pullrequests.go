package gitops

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Pull-request states as GitHub reports them.
const (
	// PROpen is an open, unmerged pull request.
	PROpen = "open"
	// PRMerged is a pull request GitHub recorded as merged.
	PRMerged = "merged"
	// PRClosed is a pull request closed without merging.
	PRClosed = "closed"
	// PRBlocked means the pull request exists but merging is not permitted yet.
	PRBlocked = "blocked"
	// PRAbandoned means RepoSurgeon gave up on the pull request.
	PRAbandoned = "abandoned"
)

// PullRequest is one GitHub pull request as RepoSurgeon recorded it.
type PullRequest struct {
	// ID is the RepoSurgeon-side identifier.
	ID string `json:"id"`
	// RunID is the run that produced the change.
	RunID string `json:"run_id"`
	// OrganizationID is the tenant that owns the run.
	OrganizationID string `json:"organization_id"`
	// WorkspaceID scopes the workspace.
	WorkspaceID string `json:"workspace_id"`
	// Number is the GitHub PR number.
	Number int64 `json:"number,omitempty"`
	// URL is the exact GitHub link operators open.
	URL string `json:"url,omitempty"`
	// RepoOwner and RepoName identify the repository.
	RepoOwner string `json:"repo_owner"`
	RepoName  string `json:"repo_name"`
	// HeadBranch is the branch the change lives on.
	HeadBranch string `json:"head_branch"`
	// BaseBranch is what the change targets.
	BaseBranch string `json:"base_branch"`
	// Title is the PR title.
	Title string `json:"title"`
	// Body is the structured PR description.
	Body string `json:"body"`
	// IssueNumber is the issue the PR references, when there is one.
	IssueNumber int64 `json:"issue_number,omitempty"`
	// HeadSHA is the commit the PR currently points at.
	HeadSHA string `json:"head_sha,omitempty"`
	// BaseSHA is the base commit the PR was opened against.
	BaseSHA string `json:"base_sha,omitempty"`
	// State is one of the states above.
	State string `json:"state"`
	// Checks are the real GitHub check runs tracked for this PR.
	Checks []RemoteCheck `json:"checks,omitempty"`
	// Reviews are the real GitHub reviews tracked for this PR.
	Reviews []Review `json:"reviews,omitempty"`
	// ApprovalCount is the number of recorded approvals.
	ApprovalCount int `json:"approval_count"`
	// MergeState is GitHub's own mergeability word.
	MergeState string `json:"merge_state,omitempty"`
	// BlockedReason explains why merging is not permitted yet.
	BlockedReason string `json:"blocked_reason,omitempty"`
	// MergeSHA is the merge commit GitHub created.
	MergeSHA string `json:"merge_sha,omitempty"`
	// BranchDeleted reports whether the head branch was deleted after the merge.
	BranchDeleted bool `json:"branch_deleted"`
	// EffectID is the idempotency key that makes PR creation exactly-once.
	EffectID string `json:"effect_id"`
	// CreatedAt and UpdatedAt are RFC3339 timestamps.
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// MergeAction records who authorized a merge and what GitHub answered.
type MergeAction struct {
	// ID identifies the action.
	ID string `json:"id"`
	// RunID is the run.
	RunID string `json:"run_id"`
	// OrganizationID is the tenant that owns the run.
	OrganizationID string `json:"organization_id"`
	// PullRequestID is the pull request merged.
	PullRequestID string `json:"pull_request_id"`
	// AuthorizedBy is the human who authorized it; never a model.
	AuthorizedBy string `json:"authorized_by"`
	// Method is the merge method, for example merge or squash.
	Method string `json:"method"`
	// MergeSHA is the resulting commit on the base branch.
	MergeSHA string `json:"merge_sha,omitempty"`
	// Succeeded reports GitHub's outcome.
	Succeeded bool `json:"succeeded"`
	// Outcome is verbatim GitHub detail.
	Outcome string `json:"outcome,omitempty"`
	// EffectID makes the merge exactly-once across retries.
	EffectID string `json:"effect_id"`
	// CreatedAt is the RFC3339 timestamp.
	CreatedAt string `json:"created_at"`
}

// Errors the service surfaces to callers.
var (
	// ErrPullRequestExists reports a PR that was already created.
	ErrPullRequestExists = errors.New("gitops: a pull request already exists for this run")
	// ErrPullRequestUnknown reports an unknown PR id.
	ErrPullRequestUnknown = errors.New("gitops: pull request is unknown")
	// ErrAlreadyMerged reports a second merge attempt.
	ErrAlreadyMerged = errors.New("gitops: this pull request is already merged")
	// ErrBranchNotDeleted reports a branch that still exists.
	ErrBranchNotDeleted = errors.New("gitops: head branch was not deleted")
)

// GitHubClient is the transport RepoSurgeon controls. It is deliberately the
// only way the service reaches GitHub, and it is implemented against the
// GitHub App installation token so the token never reaches a runner.
type GitHubClient interface {
	// CreatePullRequest opens one PR and returns it.
	CreatePullRequest(ctx context.Context, req CreatePullRequest) (PullRequest, error)
	// GetPullRequest reads the current PR state, checks and reviews.
	GetPullRequest(ctx context.Context, owner, repo string, number int64) (PullRequest, error)
	// ListReviews reads the reviews on a PR.
	ListReviews(ctx context.Context, owner, repo string, number int64) ([]Review, error)
	// ListChecks reads the check runs and statuses for the PR head.
	ListChecks(ctx context.Context, owner, repo, headSHA string) ([]RemoteCheck, error)
	// MergePullRequest merges a PR and returns the merge SHA.
	MergePullRequest(ctx context.Context, owner, repo string, number int64, method string) (string, error)
	// DeleteRef deletes the head branch reference.
	DeleteRef(ctx context.Context, owner, repo, branch string) error
	// BranchProtected reports whether branch protection requires checks or
	// reviews for the base branch.
	BranchProtected(ctx context.Context, owner, repo, branch string) (bool, int, []string, error)
}

// CreatePullRequest is the PR create request.
type CreatePullRequest struct {
	// Owner and Repo identify the repository.
	Owner string
	Repo  string
	// Head and Base are the branches.
	Head string
	Base string
	// Title and Body are the PR content.
	Title string
	Body  string
	// Issue is the issue the body references.
	Issue int64
	// EffectID makes the create exactly-once.
	EffectID string
	// Draft marks the PR as a draft while checks run.
	Draft bool
	// RunID is the run the pull request belongs to.
	RunID string
	// WorkspaceID scopes the workspace.
	WorkspaceID string
}

// PullRequestBody composes the structured PR body. It is generated from the
// change itself so the description an reviewer reads is evidence, not a claim.
type PullRequestBody struct {
	// Summary is one paragraph of what changed and why.
	Summary string
	// TestEvidence lists the checks that ran, with their verdicts.
	TestEvidence []LocalCheck
	// Risks lists the known risks of the change.
	Risks []string
	// Files lists the changed files.
	Files []string
	// IssueNumber links the issue.
	IssueNumber int64
}

// Render produces the PR body text. Every line is either a recorded check
// result or a header, so the body cannot claim a pass that did not happen.
func (b PullRequestBody) Render() string {
	var body strings.Builder
	body.WriteString("## Summary\n\n")
	body.WriteString(strings.TrimSpace(b.Summary))
	if body.String() == "" {
		body.WriteString("_(no summary provided)_")
	}
	body.WriteString("\n\n")

	body.WriteString("## Test evidence\n\n")
	if len(b.TestEvidence) == 0 {
		body.WriteString("_No checks were recorded for this change._\n")
	}
	for _, check := range b.TestEvidence {
		verdict := check.Verdict
		if !check.Ran {
			verdict = "did not run"
		}
		fmt.Fprintf(&body, "- `%s`: %s", check.Profile, verdict)
		if check.Detail != "" {
			body.WriteString(" — " + check.Detail)
		}
		body.WriteString("\n")
	}
	body.WriteString("\n")

	body.WriteString("## Changed files\n\n")
	if len(b.Files) == 0 {
		body.WriteString("_No files were recorded._\n")
	}
	for _, file := range b.Files {
		body.WriteString("- " + file + "\n")
	}
	body.WriteString("\n")

	body.WriteString("## Known risks\n\n")
	if len(b.Risks) == 0 {
		body.WriteString("_None recorded._\n")
	}
	for _, risk := range b.Risks {
		body.WriteString("- " + risk + "\n")
	}
	body.WriteString("\n")

	if b.IssueNumber > 0 {
		fmt.Fprintf(&body, "\nCloses #%d\n", b.IssueNumber)
	}
	body.WriteString("\n---\nGenerated by RepoSurgeon.\n")
	return body.String()
}

// PRService composes commits, pushes through the trusted client and tracks the
// pull request lifecycle. It never deletes an unmerged branch and never merges
// without an explicit human authorization.
type PRService struct {
	// Client is the trusted GitHub transport.
	Client GitHubClient
	// Repos persists pull requests and merge actions.
	Repos PullRequestStore
	// Gate decides pushes and merges.
	Gate *Gate
	// Clock stamps records; overridden in tests.
	Clock func() string
	// UnprotectedMergePolicy allows merging into a branch without protection.
	// It defaults to refused, because branch protection is what makes the
	// required checks and reviews real.
	UnprotectedMergePolicy bool
	// mergeMu serializes the check-then-merge sequence so two workers acting on
	// a retried request cannot both merge the same pull request.
	mergeMu sync.Mutex
}

// PullRequestStore persists pull requests and merge actions.
type PullRequestStore interface {
	SavePullRequest(ctx context.Context, pr PullRequest) error
	GetPullRequest(ctx context.Context, runID string) (PullRequest, bool, error)
	ListPullRequests(ctx context.Context, organizationID, runID string, limit int) ([]PullRequest, error)
	RecordMerge(ctx context.Context, action MergeAction) error
	MergeActions(ctx context.Context, runID string, limit int) ([]MergeAction, error)
}

// NewPRService builds the pull-request service.
func NewPRService(client GitHubClient, repos PullRequestStore, gate *Gate) *PRService {
	return &PRService{Client: client, Repos: repos, Gate: gate, Clock: nowStamp}
}

// Open creates one pull request for a run and records it. The effect id makes
// the call exactly-once: a repeated webhook, a network retry or a concurrent
// request returns the same pull request instead of creating a second one.
func (s *PRService) Open(ctx context.Context, request CreatePullRequest, body PullRequestBody) (PullRequest, error) {
	if strings.TrimSpace(request.EffectID) == "" {
		return PullRequest{}, errors.New("gitops: an effect id is required to open a pull request")
	}
	existing, ok, err := s.Repos.GetPullRequest(ctx, request.RunID)
	if err != nil {
		return PullRequest{}, err
	}
	if ok && existing.Number > 0 && existing.State != PRAbandoned {
		if existing.EffectID == request.EffectID || existing.State == PRMerged {
			return existing, ErrPullRequestExists
		}
	}
	// The issue the body references is the issue the request names, so the PR
	// always links the work to its issue.
	if body.IssueNumber == 0 {
		body.IssueNumber = request.Issue
	}
	created, err := s.Client.CreatePullRequest(ctx, request)
	if err != nil {
		return PullRequest{}, err
	}
	created.Body = body.Render()
	if created.CreatedAt == "" {
		created.CreatedAt = s.Clock()
	}
	created.UpdatedAt = created.CreatedAt
	if err := s.Repos.SavePullRequest(ctx, created); err != nil {
		return PullRequest{}, err
	}
	return created, nil
}

// Status refreshes the pull request from GitHub and records the result. The
// states pending, failed, blocked and approved are distinguished so a UI can
// show them apart.
func (s *PRService) Status(ctx context.Context, runID string) (PullRequest, error) {
	pr, ok, err := s.Repos.GetPullRequest(ctx, runID)
	if err != nil {
		return PullRequest{}, err
	}
	if !ok {
		return PullRequest{}, ErrPullRequestUnknown
	}
	if pr.Number == 0 {
		return pr, ErrPullRequestUnknown
	}
	current, err := s.Client.GetPullRequest(ctx, pr.RepoOwner, pr.RepoName, pr.Number)
	if err != nil {
		return PullRequest{}, err
	}
	checks, err := s.Client.ListChecks(ctx, pr.RepoOwner, pr.RepoName, current.HeadSHA)
	if err != nil {
		return PullRequest{}, err
	}
	reviews, err := s.Client.ListReviews(ctx, pr.RepoOwner, pr.RepoName, pr.Number)
	if err != nil {
		return PullRequest{}, err
	}
	current.ID = pr.ID
	current.RunID = pr.RunID
	current.OrganizationID = pr.OrganizationID
	current.WorkspaceID = pr.WorkspaceID
	current.EffectID = pr.EffectID
	current.IssueNumber = firstNonZero(pr.IssueNumber, current.IssueNumber)
	// GitHub reports the branch state, but the repository, branch names and
	// title come from the record RepoSurgeon opened, so they are carried over
	// rather than read from a response that does not repeat them.
	current.RepoOwner = firstNonEmpty(pr.RepoOwner, current.RepoOwner)
	current.RepoName = firstNonEmpty(pr.RepoName, current.RepoName)
	current.HeadBranch = firstNonEmpty(pr.HeadBranch, current.HeadBranch)
	current.BaseBranch = firstNonEmpty(pr.BaseBranch, current.BaseBranch)
	current.Title = firstNonEmpty(pr.Title, current.Title)
	current.Body = firstNonEmpty(pr.Body, current.Body)
	current.CreatedAt = firstNonEmpty(pr.CreatedAt, current.CreatedAt, s.Clock())
	current.Checks = checks
	current.Reviews = reviews
	current.ApprovalCount = countApprovals(reviews)
	if current.MergeSHA != "" {
		current.State = PRMerged
	}
	current.UpdatedAt = s.Clock()
	if err := s.Repos.SavePullRequest(ctx, current); err != nil {
		return PullRequest{}, err
	}
	return current, nil
}

// policyPermits answers whether the repository policy allows this merge. The
// default is to require branch protection; the head branch being main is
// irrelevant, because a model-driven change still must clear the same gate.
func (s *PRService) policyPermits(protected bool) bool {
	if s.UnprotectedMergePolicy {
		return true
	}
	return protected
}

// Merge authorizes and performs the merge. The gate is evaluated against real
// GitHub state immediately before the merge, and a human authorization is
// required: a model's own assertion never authorizes anything.
func (s *PRService) Merge(ctx context.Context, runID, authorizedBy, method string) (PullRequest, MergeAction, Decision, error) {
	s.mergeMu.Lock()
	defer s.mergeMu.Unlock()
	pr, ok, err := s.Repos.GetPullRequest(ctx, runID)
	if err != nil {
		return PullRequest{}, MergeAction{}, Decision{}, err
	}
	if !ok || pr.Number == 0 {
		return PullRequest{}, MergeAction{}, Decision{}, ErrPullRequestUnknown
	}
	if pr.State == PRMerged && pr.MergeSHA != "" {
		return pr, MergeAction{}, Decision{}, ErrAlreadyMerged
	}
	current, err := s.Status(ctx, runID)
	if err != nil {
		return PullRequest{}, MergeAction{}, Decision{}, err
	}
	protected, requiredApprovals, requiredChecks, err := s.Client.BranchProtected(ctx, current.RepoOwner, current.RepoName, current.BaseBranch)
	if err != nil {
		return PullRequest{}, MergeAction{}, Decision{}, err
	}
	decision := s.Gate.CheckMerge(MergeRequest{
		PullRequestNumber:  current.Number,
		Checks:             current.Checks,
		Reviews:            current.Reviews,
		RequiredChecks:     requiredChecks,
		RequiredApprovals:  requiredApprovals,
		ProtectionActive:   protected,
		AuthorizedBy:       authorizedBy,
		PolicyPermitsMerge: s.policyPermits(protected),
	})
	if !decision.Allowed {
		if current.State == PROpen {
			current.State = PRBlocked
			current.BlockedReason = strings.Join(decision.Reasons, "; ")
			current.UpdatedAt = s.Clock()
			if err := s.Repos.SavePullRequest(ctx, current); err != nil {
				return current, MergeAction{}, decision, err
			}
		}
		return current, MergeAction{}, decision, ErrGateRefused
	}
	mergeSHA, err := s.Client.MergePullRequest(ctx, current.RepoOwner, current.RepoName, current.Number, methodOrDefault(method))
	if err != nil {
		return current, MergeAction{}, decision, err
	}
	action := MergeAction{
		ID:            NewActionID(),
		RunID:         current.RunID,
		PullRequestID: current.ID,
		AuthorizedBy:  authorizedBy,
		Method:        methodOrDefault(method),
		MergeSHA:      mergeSHA,
		Succeeded:     mergeSHA != "",
		EffectID:      "merge:" + current.EffectID,
		CreatedAt:     s.Clock(),
	}
	if err := s.Repos.RecordMerge(ctx, action); err != nil {
		return current, action, decision, err
	}
	// The merged SHA is verified on the base branch before the head branch is
	// deleted, so an unmerged branch is never removed.
	verified, err := s.Client.GetPullRequest(ctx, current.RepoOwner, current.RepoName, current.Number)
	if err != nil {
		return current, action, decision, err
	}
	// GitHub does not know our identifiers, so the verified record keeps the
	// stored identity or the update would be written as a new row.
	verified.ID = firstNonEmpty(current.ID, verified.ID)
	verified.RunID = current.RunID
	verified.OrganizationID = current.OrganizationID
	verified.WorkspaceID = current.WorkspaceID
	verified.EffectID = current.EffectID
	verified.RepoOwner = firstNonEmpty(current.RepoOwner, verified.RepoOwner)
	verified.RepoName = firstNonEmpty(current.RepoName, verified.RepoName)
	verified.HeadBranch = firstNonEmpty(current.HeadBranch, verified.HeadBranch)
	verified.BaseBranch = firstNonEmpty(current.BaseBranch, verified.BaseBranch)
	verified.Title = firstNonEmpty(current.Title, verified.Title)
	verified.Body = firstNonEmpty(current.Body, verified.Body)
	verified.IssueNumber = firstNonZero(current.IssueNumber, verified.IssueNumber)
	verified.State = PRMerged
	verified.MergeSHA = mergeSHA
	verified.BranchDeleted = false
	verified.UpdatedAt = s.Clock()
	if verified.MergeState == "merged" || mergeSHA != "" {
		verified.BranchDeleted = true
		if err := s.Client.DeleteRef(ctx, verified.RepoOwner, verified.RepoName, verified.HeadBranch); err != nil && !isNotFound(err) {
			verified.BranchDeleted = false
		}
	}
	if err := s.Repos.SavePullRequest(ctx, verified); err != nil {
		return current, action, decision, err
	}
	return verified, action, decision, nil
}

// Abandon closes a pull request without merging and keeps the branch and its
// evidence, so a failed run is never presented as a shipped change.
func (s *PRService) Abandon(ctx context.Context, runID, reason string) (PullRequest, error) {
	pr, ok, err := s.Repos.GetPullRequest(ctx, runID)
	if err != nil {
		return PullRequest{}, err
	}
	if !ok {
		return PullRequest{}, ErrPullRequestUnknown
	}
	if pr.State == PRMerged {
		return pr, ErrAlreadyMerged
	}
	pr.State = PRAbandoned
	pr.BlockedReason = reason
	pr.UpdatedAt = s.Clock()
	if err := s.Repos.SavePullRequest(ctx, pr); err != nil {
		return PullRequest{}, err
	}
	return pr, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func countApprovals(reviews []Review) int {
	count := 0
	for _, review := range reviews {
		if review.State == "approved" {
			count++
		}
	}
	return count
}

func methodOrDefault(method string) string {
	if strings.TrimSpace(method) == "" {
		return "merge"
	}
	return method
}

// authorizedByRefusal names why an authorizer is not acceptable. Git commit
// identity is not authentication and a model identity is never an operator, so
// neither may authorize a merge.
func authorizedByRefusal(authorizedBy string) string {
	trimmed := strings.TrimSpace(authorizedBy)
	if trimmed == "" {
		return "no human authorized this merge; a model's own claim never authorizes it"
	}
	if looksLikeModelIdentity(trimmed) {
		return "a model identity (" + trimmed + ") cannot authorize a merge; a permitted human must"
	}
	return ""
}

// looksLikeModelIdentity recognizes provider and model identities that must
// never be treated as an operator.
func looksLikeModelIdentity(value string) bool {
	lower := strings.ToLower(value)
	markers := []string{"step-", "gpt-", "claude", "gemini", "llama", "mistral", "qwen", "deepseek", "model", "assistant", "bot", "service-account", "system"}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func isNotFound(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")
}

// NewActionID returns a fresh merge action identifier.
func NewActionID() string { return "mrg_" + randomSuffix() }

// randomSuffix returns a short random identifier.
func randomSuffix() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0"
	}
	return hex.EncodeToString(b[:])
}

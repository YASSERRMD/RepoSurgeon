package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/gitops"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PullRequests persists the M13 pull-request lifecycle. Every statement is
// scoped to an organization so one tenant cannot read another tenant's PRs.
type PullRequests struct {
	pool *pgxpool.Pool
}

// NewPullRequests binds the repository to the pool.
func NewPullRequests(pool *pgxpool.Pool) *PullRequests { return &PullRequests{pool: pool} }

// PRCommits persists the atomic commits behind a pull request.
type PRCommits struct {
	pool *pgxpool.Pool
}

// NewPRCommits binds the repository to the pool.
func NewPRCommits(pool *pgxpool.Pool) *PRCommits { return &PRCommits{pool: pool} }

// MergeActions persists human-authorized merge actions.
type MergeActions struct {
	pool *pgxpool.Pool
}

// NewMergeActions binds the repository to the pool.
func NewMergeActions(pool *pgxpool.Pool) *MergeActions { return &MergeActions{pool: pool} }

// PRStore binds the three M13 repositories into one store.
type PRStore struct {
	PRs        *PullRequests
	CommitRepo *PRCommits
	ActionRepo *MergeActions
}

// NewPRStore binds all three repositories.
func NewPRStore(pool *pgxpool.Pool) *PRStore {
	return &PRStore{
		PRs:        NewPullRequests(pool),
		CommitRepo: NewPRCommits(pool),
		ActionRepo: NewMergeActions(pool),
	}
}

const pullRequestColumns = `id, run_id, organization_id, workspace_id, number, url, repo_owner, repo_name,
       head_branch, base_branch, title, body, issue_number, head_sha, base_sha, state, checks, reviews,
       approval_count, merge_state, blocked_reason, merge_sha, branch_deleted, effect_id, created_at`

func scanPullRequest(scanner interface{ Scan(dest ...any) error }) (gitops.PullRequest, error) {
	var (
		pr      gitops.PullRequest
		checks  []byte
		reviews []byte
		created time.Time
	)
	if err := scanner.Scan(&pr.ID, &pr.RunID, &pr.OrganizationID, &pr.WorkspaceID, &pr.Number, &pr.URL,
		&pr.RepoOwner, &pr.RepoName, &pr.HeadBranch, &pr.BaseBranch, &pr.Title, &pr.Body, &pr.IssueNumber,
		&pr.HeadSHA, &pr.BaseSHA, &pr.State, &checks, &reviews, &pr.ApprovalCount, &pr.MergeState,
		&pr.BlockedReason, &pr.MergeSHA, &pr.BranchDeleted, &pr.EffectID, &created); err != nil {
		return gitops.PullRequest{}, err
	}
	if len(checks) > 0 {
		if err := json.Unmarshal(checks, &pr.Checks); err != nil {
			return gitops.PullRequest{}, fmt.Errorf("pull requests: decode checks: %w", err)
		}
	}
	if len(reviews) > 0 {
		if err := json.Unmarshal(reviews, &pr.Reviews); err != nil {
			return gitops.PullRequest{}, fmt.Errorf("pull requests: decode reviews: %w", err)
		}
	}
	pr.CreatedAt = created.UTC().Format(time.RFC3339)
	return pr, nil
}

// Save upserts one pull request. The unique constraint on (run_id, effect_id)
// is what makes a replayed create return the same row instead of a new PR.
func (r *PullRequests) Save(ctx context.Context, pr gitops.PullRequest) error {
	if pr.ID == "" || pr.RunID == "" || pr.OrganizationID == "" || pr.EffectID == "" {
		return domain.ErrValidation
	}
	checks, err := json.Marshal(pr.Checks)
	if err != nil {
		return fmt.Errorf("pull requests: marshal checks: %w", err)
	}
	reviews, err := json.Marshal(pr.Reviews)
	if err != nil {
		return fmt.Errorf("pull requests: marshal reviews: %w", err)
	}
	_, err = r.pool.Exec(ctx, `
        INSERT INTO pull_requests (id, run_id, organization_id, workspace_id, number, url, repo_owner, repo_name,
                                    head_branch, base_branch, title, body, issue_number, head_sha, base_sha, state,
                                    checks, reviews, approval_count, merge_state, blocked_reason, merge_sha,
                                    branch_deleted, effect_id)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)
        ON CONFLICT (run_id, effect_id) DO UPDATE SET
            number = excluded.number,
            url = excluded.url,
            state = excluded.state,
            checks = excluded.checks,
            reviews = excluded.reviews,
            approval_count = excluded.approval_count,
            merge_state = excluded.merge_state,
            blocked_reason = excluded.blocked_reason,
            merge_sha = excluded.merge_sha,
            branch_deleted = excluded.branch_deleted,
            updated_at = now()`,
		pr.ID, pr.RunID, pr.OrganizationID, pr.WorkspaceID, pr.Number, pr.URL, pr.RepoOwner, pr.RepoName,
		pr.HeadBranch, pr.BaseBranch, pr.Title, pr.Body, pr.IssueNumber, pr.HeadSHA, pr.BaseSHA, pr.State,
		checks, reviews, pr.ApprovalCount, pr.MergeState, pr.BlockedReason, pr.MergeSHA, pr.BranchDeleted,
		pr.EffectID)
	if err != nil {
		return fmt.Errorf("pull requests: upsert: %w", err)
	}
	return nil
}

// Get reads one pull request scoped to its organization.
func (r *PullRequests) Get(ctx context.Context, organizationID, runID string) (gitops.PullRequest, bool, error) {
	row := r.pool.QueryRow(ctx, `
        SELECT `+pullRequestColumns+`
        FROM pull_requests WHERE organization_id = $1 AND run_id = $2
        ORDER BY created_at DESC LIMIT 1`, organizationID, runID)
	pr, err := scanPullRequest(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return gitops.PullRequest{}, false, nil
	}
	if err != nil {
		return gitops.PullRequest{}, false, fmt.Errorf("pull requests: get: %w", err)
	}
	return pr, true, nil
}

// organizationOf resolves the tenant of a run, so the store never trusts a
// caller-supplied organization id for a cross-tenant lookup.
func (r *PullRequests) organizationOf(ctx context.Context, runID string) (string, error) {
	var organizationID string
	if err := r.pool.QueryRow(ctx, `SELECT organization_id FROM runs WHERE id = $1`, runID).Scan(&organizationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrNotFound
		}
		return "", fmt.Errorf("pull requests: resolve organization: %w", err)
	}
	return organizationID, nil
}

// GetByRun reads the newest pull request of a run with the organization
// resolved from the run itself.
func (r *PullRequests) GetByRun(ctx context.Context, runID string) (gitops.PullRequest, bool, error) {
	organizationID, err := r.organizationOf(ctx, runID)
	if err != nil {
		return gitops.PullRequest{}, false, err
	}
	return r.Get(ctx, organizationID, runID)
}

// List returns the pull requests of a run, newest first.
func (r *PullRequests) List(ctx context.Context, organizationID, runID string, limit int) ([]gitops.PullRequest, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
        SELECT `+pullRequestColumns+`
        FROM pull_requests WHERE organization_id = $1 AND run_id = $2
        ORDER BY created_at DESC LIMIT $3`, organizationID, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("pull requests: list: %w", err)
	}
	defer rows.Close()
	out := []gitops.PullRequest{}
	for rows.Next() {
		pr, err := scanPullRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("pull requests: scan: %w", err)
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// Save stores one composed commit behind a pull request.
func (r *PRCommits) Save(ctx context.Context, commit gitops.Commit) error {
	if commit.SHA == "" || commit.RunID == "" {
		return domain.ErrValidation
	}
	_, err := r.pool.Exec(ctx, `
        INSERT INTO pr_commits (id, pull_request_id, organization_id, run_id, sha, message,
                                author_name, author_email, committer_name, committer_email, files)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
        ON CONFLICT (pull_request_id, sha) DO NOTHING`,
		commit.PullRequestID+"_"+commit.SHA, commit.PullRequestID, commit.OrganizationID, commit.RunID,
		commit.SHA, commit.Message, commit.Author.Name, commit.Author.Email, commit.Committer.Name,
		commit.Committer.Email, commit.Files)
	if err != nil {
		return fmt.Errorf("pr commits: insert: %w", err)
	}
	return nil
}

// List returns the composed commits of a pull request, oldest first.
func (r *PRCommits) List(ctx context.Context, organizationID, prID string, limit int) ([]gitops.Commit, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
        SELECT sha, message, author_name, author_email, committer_name, committer_email, files
        FROM pr_commits WHERE organization_id = $1 AND pull_request_id = $2
        ORDER BY created_at ASC LIMIT $3`, organizationID, prID, limit)
	if err != nil {
		return nil, fmt.Errorf("pr commits: query: %w", err)
	}
	defer rows.Close()
	out := []gitops.Commit{}
	for rows.Next() {
		var commit gitops.Commit
		if err := rows.Scan(&commit.SHA, &commit.Message, &commit.Author.Name, &commit.Author.Email,
			&commit.Committer.Name, &commit.Committer.Email, &commit.Files); err != nil {
			return nil, fmt.Errorf("pr commits: scan: %w", err)
		}
		out = append(out, commit)
	}
	return out, rows.Err()
}

// Record stores one merge action. The unique constraint on (run_id, effect_id)
// makes a replayed merge a no-op rather than a second merge.
func (r *MergeActions) Record(ctx context.Context, action gitops.MergeAction) error {
	if action.ID == "" || action.RunID == "" || action.OrganizationID == "" || action.EffectID == "" {
		return domain.ErrValidation
	}
	_, err := r.pool.Exec(ctx, `
        INSERT INTO merge_actions (id, run_id, organization_id, pull_request_id, authorized_by, method,
                                   merge_sha, succeeded, outcome, effect_id)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
        ON CONFLICT (run_id, effect_id) DO NOTHING`,
		action.ID, action.RunID, action.OrganizationID, action.PullRequestID, action.AuthorizedBy,
		action.Method, action.MergeSHA, action.Succeeded, action.Outcome, action.EffectID)
	if err != nil {
		return fmt.Errorf("merge actions: insert: %w", err)
	}
	return nil
}

// List returns the merge actions of a run, oldest first.
func (r *MergeActions) List(ctx context.Context, runID string, limit int) ([]gitops.MergeAction, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	rows, err := r.pool.Query(ctx, `
        SELECT id, run_id, organization_id, pull_request_id, authorized_by, method, merge_sha, succeeded,
               outcome, effect_id, created_at
        FROM merge_actions WHERE run_id = $1 ORDER BY created_at ASC LIMIT $2`, runID, limit)
	if err != nil {
		return nil, fmt.Errorf("merge actions: query: %w", err)
	}
	defer rows.Close()
	out := []gitops.MergeAction{}
	for rows.Next() {
		var (
			action  gitops.MergeAction
			created time.Time
		)
		if err := rows.Scan(&action.ID, &action.RunID, &action.OrganizationID, &action.PullRequestID,
			&action.AuthorizedBy, &action.Method, &action.MergeSHA, &action.Succeeded, &action.Outcome,
			&action.EffectID, &created); err != nil {
			return nil, fmt.Errorf("merge actions: scan: %w", err)
		}
		action.CreatedAt = created.UTC().Format(time.RFC3339)
		out = append(out, action)
	}
	return out, rows.Err()
}

// SavePullRequest stores one pull request for the store interface.
func (s *PRStore) SavePullRequest(ctx context.Context, pr gitops.PullRequest) error {
	return s.PRs.Save(ctx, pr)
}

// GetPullRequest reads the newest pull request of a run.
func (s *PRStore) GetPullRequest(ctx context.Context, runID string) (gitops.PullRequest, bool, error) {
	return s.PRs.GetByRun(ctx, runID)
}

// ListPullRequests lists the pull requests of a run.
func (s *PRStore) ListPullRequests(ctx context.Context, organizationID, runID string, limit int) ([]gitops.PullRequest, error) {
	return s.PRs.List(ctx, organizationID, runID, limit)
}

// SaveCommit stores one composed commit.
func (s *PRStore) SaveCommit(ctx context.Context, prID, runID, organizationID string, commit gitops.Commit) error {
	commit.PullRequestID = prID
	commit.RunID = runID
	commit.OrganizationID = organizationID
	return s.CommitRepo.Save(ctx, commit)
}

// RecordMerge stores one human-authorized merge.
func (s *PRStore) RecordMerge(ctx context.Context, action gitops.MergeAction) error {
	if action.OrganizationID == "" {
		organizationID, err := s.PRs.organizationOf(ctx, action.RunID)
		if err != nil {
			return err
		}
		action.OrganizationID = organizationID
	}
	return s.ActionRepo.Record(ctx, action)
}

// MergeActions lists the merge actions of a run.
func (s *PRStore) MergeActions(ctx context.Context, runID string, limit int) ([]gitops.MergeAction, error) {
	return s.ActionRepo.List(ctx, runID, limit)
}

// Commits lists the composed commits of a pull request.
func (s *PRStore) Commits(ctx context.Context, organizationID, prID string, limit int) ([]gitops.Commit, error) {
	return s.CommitRepo.List(ctx, organizationID, prID, limit)
}

// Package gitops turns a run's workspace into a reviewable change: atomic
// commits with an enforced identity, a gate that decides whether pushing and
// merging are allowed, and the pull-request lifecycle states that GitHub
// reports back. Nothing here talks to GitHub on its own; the caller supplies
// the transport, so every decision is testable without network access.
package gitops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Identity is the author and committer RepoSurgeon-controlled commits must
// carry. It is enforced here and verified after every commit, because a commit
// identity is not an authentication identity: it never authorizes anything.
type Identity struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// DefaultIdentity is the identity of record for RepoSurgeon commits.
func DefaultIdentity() Identity {
	return Identity{Name: "yasserrmd", Email: "arafath.yasser@gmail.com"}
}

// Signing configures commit signing. RepoSurgeon commits are never signed with
// a key the platform does not already hold, so signing stays disabled unless a
// caller explicitly provides a key.
type Signing struct {
	Key string
}

// ErrIdentityMismatch reports a commit whose recorded identity is not the one
// RepoSurgeon is configured to use.
var ErrIdentityMismatch = errors.New("gitops: commit identity mismatch")

// ErrNothingToCommit reports a commit group with no staged content.
var ErrNothingToCommit = errors.New("gitops: nothing to commit for this group")

// CommitSpec is one logical change: what it touches and why.
type CommitSpec struct {
	// Message is the commit subject plus optional body.
	Message string `json:"message"`
	// Paths are the files this commit contains.
	Paths []string `json:"paths"`
}

// Commit is the verified result of one atomic commit.
type Commit struct {
	// PullRequestID is the pull request the commit belongs to.
	PullRequestID string `json:"pull_request_id"`
	// RunID is the run the commit belongs to.
	RunID string `json:"run_id"`
	// OrganizationID is the tenant that owns the run.
	OrganizationID string `json:"organization_id"`
	// SHA is the commit the repository actually created.
	SHA string `json:"sha"`
	// Message is the message recorded in the commit.
	Message string `json:"message"`
	// Author is the identity the commit recorded.
	Author Identity `json:"author"`
	// Committer is the identity the commit recorded.
	Committer Identity `json:"committer"`
	// Files are the paths the commit changed.
	Files []string `json:"files"`
	// CreatedAt is when RepoSurgeon observed the commit.
	CreatedAt string `json:"created_at"`
}

// Composer builds atomic commits in a workspace.
type Composer struct {
	// Identity is enforced for every commit.
	Identity Identity
	// Signing is empty for unsigned commits.
	Signing Signing
	// git runs shell git; it is a field so tests can point at a fixture binary.
	git func(ctx context.Context, dir string, args ...string) (string, error)
}

// NewComposer builds a composer with the identity of record.
func NewComposer() *Composer {
	return &Composer{Identity: DefaultIdentity()}
}

// CommitAll composes every spec in order and returns the verified commits.
//
// A spec whose paths staged nothing is reported as skipped rather than
// silently dropped: the caller must know that a logical change it asked for
// produced no commit.
func (c *Composer) CommitAll(ctx context.Context, dir string, specs []CommitSpec) ([]Commit, []CommitSpec, error) {
	committed := make([]Commit, 0, len(specs))
	skipped := make([]CommitSpec, 0)
	for _, spec := range specs {
		if strings.TrimSpace(spec.Message) == "" {
			return committed, skipped, errors.New("gitops: commit message is required")
		}
		if len(spec.Paths) == 0 {
			return committed, skipped, errors.New("gitops: commit paths are required")
		}
		// Only paths that exist on disk are staged: a spec naming something
		// that is not there is reported as skipped, never silently committed
		// and never allowed to abort the whole composition.
		present := existingPaths(dir, spec.Paths)
		if len(present) == 0 {
			skipped = append(skipped, spec)
			continue
		}
		args := append([]string{"add", "--"}, present...)
		if _, err := c.runGit(ctx, dir, args...); err != nil {
			return committed, skipped, err
		}
		if _, err := c.runGit(ctx, dir, "diff", "--cached", "--quiet"); err == nil {
			skipped = append(skipped, spec)
			continue
		}
		sha, err := c.commit(ctx, dir, spec.Message, spec.Paths)
		if err != nil {
			return committed, skipped, err
		}
		commit, err := c.verify(ctx, dir, sha, spec)
		if err != nil {
			return committed, skipped, err
		}
		committed = append(committed, commit)
	}
	return committed, skipped, nil
}

// HeadSHA returns the current commit of the workspace.
func (c *Composer) HeadSHA(ctx context.Context, dir string) (string, error) {
	out, err := c.runGit(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Branch returns the current branch name.
func (c *Composer) Branch(ctx context.Context, dir string) (string, error) {
	out, err := c.runGit(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(out)
	if branch == "" || branch == "HEAD" {
		return "", errors.New("gitops: workspace is on a detached HEAD")
	}
	return branch, nil
}

// ChangedFilesSince returns the paths that differ from the given ref.
func (c *Composer) ChangedFilesSince(ctx context.Context, dir, ref string) ([]string, error) {
	out, err := c.runGit(ctx, dir, "diff", "--name-only", ref+"...HEAD")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			files = append(files, trimmed)
		}
	}
	return files, nil
}

// PushedRefsReachable reports whether the branch commit exists on any remote
// ref, so an already-pushed branch is not force-rewritten by a later attempt.
func (c *Composer) PushedRefsReachable(ctx context.Context, dir, sha string) (bool, error) {
	if _, err := c.runGit(ctx, dir, "cat-file", "-e", sha+"^{commit}"); err != nil {
		return false, fmt.Errorf("gitops: unknown commit %s: %w", short(sha), err)
	}
	if _, err := c.runGit(ctx, dir, "branch", "-r", "--contains", sha); err == nil {
		out, err := c.runGit(ctx, dir, "for-each-ref", "--count=1", "--contains", sha, "refs/remotes")
		if err == nil && strings.TrimSpace(out) != "" {
			return true, nil
		}
	}
	return false, nil
}

func (c *Composer) commit(ctx context.Context, dir, message string, paths []string) (string, error) {
	args := make([]string, 0, 12+len(paths))
	args = append(args,
		"-c", "user.name="+c.Identity.Name,
		"-c", "user.email="+c.Identity.Email,
		"-c", "commit.gpgsign=false",
		"commit", "--quiet", "--no-verify", "-m", message,
		"--",
	)
	args = append(args, paths...)
	out, err := c.runGit(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	_ = out
	return c.HeadSHA(ctx, dir)
}

// verify reads the commit back and refuses a mismatch, because a repository can
// be configured with a different identity than the one RepoSurgeon requires.
func (c *Composer) verify(ctx context.Context, dir, sha string, spec CommitSpec) (Commit, error) {
	out, err := c.runGit(ctx, dir, "show", "-s", "--format=%an%n%ae%n%cn%n%ce%n%s", sha)
	if err != nil {
		return Commit{}, err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 5 {
		return Commit{}, fmt.Errorf("%w: cannot read commit %s", ErrIdentityMismatch, short(sha))
	}
	author := Identity{Name: lines[0], Email: lines[1]}
	committer := Identity{Name: lines[2], Email: lines[3]}
	subject := lines[4]
	if author != c.Identity {
		return Commit{}, fmt.Errorf("%w: author is %s <%s>, want %s <%s>", ErrIdentityMismatch, author.Name, author.Email, c.Identity.Name, c.Identity.Email)
	}
	if committer != c.Identity {
		return Commit{}, fmt.Errorf("%w: committer is %s <%s>, want %s <%s>", ErrIdentityMismatch, committer.Name, committer.Email, c.Identity.Name, c.Identity.Email)
	}
	filesOut, err := c.runGit(ctx, dir, "show", "--name-only", "--format=", sha)
	if err != nil {
		return Commit{}, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(filesOut), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			files = append(files, trimmed)
		}
	}
	if subject != strings.SplitN(spec.Message, "\n", 2)[0] {
		return Commit{}, fmt.Errorf("%w: subject is %q", ErrIdentityMismatch, subject)
	}
	return Commit{
		SHA:       sha,
		Message:   spec.Message,
		Author:    author,
		Committer: committer,
		Files:     files,
		CreatedAt: nowStamp(),
	}, nil
}

func (c *Composer) runGit(ctx context.Context, dir string, args ...string) (string, error) {
	if c.git != nil {
		return c.git(ctx, dir, args...)
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(timeoutCtx, "git", args...) // #nosec G204 -- git runs inside a validated workspace directory
	cmd.Dir = dir                                          // #nosec G122 -- workspace root provided by the caller
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("gitops: git %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func nowStamp() string { return time.Now().UTC().Format(time.RFC3339) }

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// existingPaths keeps the paths that exist under dir, so a spec that names a
// path which was never written is reported rather than staged.
func existingPaths(dir string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(trimmed))); err == nil {
			out = append(out, trimmed)
		}
	}
	return out
}

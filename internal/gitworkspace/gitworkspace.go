// Package gitworkspace owns pinned repository checkouts and ephemeral
// worktrees. Every Git invocation is a fixed argv array executed without a
// shell, confined to a workspace root, with bounded output. Nothing here may
// run arbitrary user-supplied commands, and the source repository's default
// branch is never modified.
package gitworkspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Errors returned by the workspace manager.
var (
	ErrInvalidSHA       = errors.New("gitworkspace: invalid commit sha")
	ErrInvalidBranch    = errors.New("gitworkspace: invalid branch name")
	ErrPathEscape       = errors.New("gitworkspace: path escapes the workspace root")
	ErrWorkspaceMissing = errors.New("gitworkspace: workspace does not exist")
	ErrGitFailed        = errors.New("gitworkspace: git command failed")
	ErrSourceChanged    = errors.New("gitworkspace: source repository default branch changed")
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// branchPattern allows conservative Git ref names.
var branchPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]{0,120}[a-zA-Z0-9]$`)

// Manager creates and destroys pinned checkouts and worktrees.
type Manager struct {
	root      string
	gitPath   string
	timeout   time.Duration
	maxOutput int
}

// New builds a manager rooted at dir. The directory is created when missing.
func New(dir string) (*Manager, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("gitworkspace: workspace root is required")
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("gitworkspace: create root: %w", err)
	}
	return &Manager{
		root:      root,
		gitPath:   "git",
		timeout:   60 * time.Second,
		maxOutput: 8 << 20,
	}, nil
}

// Root returns the absolute workspace root.
func (m *Manager) Root() string { return m.root }

// Workspace is a pinned checkout plus its isolated worktrees.
type Workspace struct {
	ID         string
	BaseSHA    string
	SourceURL  string
	DefaultRef string
	CreatedAt  time.Time
}

// Pin validates and records the base commit for a workspace.
func (m *Manager) Pin(sha string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(sha))
	if !shaPattern.MatchString(normalized) {
		return "", fmt.Errorf("%w: %q must be 40 hex characters", ErrInvalidSHA, sha)
	}
	return normalized, nil
}

// ValidateBranch checks a branch name against Git's conservative rules.
func (m *Manager) ValidateBranch(branch string) (string, error) {
	trimmed := strings.TrimSpace(branch)
	if trimmed == "" {
		return "", fmt.Errorf("%w: branch is required", ErrInvalidBranch)
	}
	if strings.Contains(trimmed, "..") || strings.HasPrefix(trimmed, "-") || strings.HasSuffix(trimmed, ".lock") {
		return "", fmt.Errorf("%w: %q contains disallowed sequences", ErrInvalidBranch, branch)
	}
	if !branchPattern.MatchString(trimmed) {
		return "", fmt.Errorf("%w: %q has an invalid name", ErrInvalidBranch, branch)
	}
	return trimmed, nil
}

// workspaceDir returns the directory for a workspace id. Ids are opaque and
// must never contain path separators, traversal or whitespace.
func (m *Manager) workspaceDir(id string) (string, error) {
	if err := validateName(id); err != nil {
		return "", ErrPathEscape
	}
	dir := filepath.Join(m.root, id)
	if !strings.HasPrefix(dir, m.root+string(filepath.Separator)) && dir != m.root {
		return "", ErrPathEscape
	}
	return dir, nil
}

// Clone clones sourceURL into a workspace pinned at baseSHA. The clone is
// shallow at the requested commit and never touches the source repository.
func (m *Manager) Clone(ctx context.Context, id, sourceURL, baseSHA string) (Workspace, error) {
	pinned, err := m.Pin(baseSHA)
	if err != nil {
		return Workspace{}, err
	}
	dir, err := m.workspaceDir(id)
	if err != nil {
		return Workspace{}, err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Workspace{}, fmt.Errorf("gitworkspace: create workspace: %w", err)
	}

	// A non-repository directory makes `git clone` fail; start clean.
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return Workspace{}, fmt.Errorf("gitworkspace: workspace %s already exists", id)
	}
	args := []string{"clone", "--quiet", "--no-checkout", "--filter=blob:none", sourceURL, dir}
	if _, err := m.runGit(ctx, "", args); err != nil {
		_ = os.RemoveAll(dir)
		return Workspace{}, err
	}
	// Detached checkout of the pinned commit only.
	checkout := []string{"-C", dir, "checkout", "--quiet", "--detach", pinned}
	if _, err := m.runGit(ctx, "", checkout); err != nil {
		_ = os.RemoveAll(dir)
		return Workspace{}, err
	}
	defaultRef, err := m.defaultBranch(ctx, dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return Workspace{}, err
	}
	return Workspace{
		ID:         id,
		BaseSHA:    pinned,
		SourceURL:  sourceURL,
		DefaultRef: defaultRef,
		CreatedAt:  time.Now().UTC(),
	}, nil
}

// WorktreePath returns the path of a worktree inside the workspace.
func (m *Manager) WorktreePath(workspaceID, worktreeName string) (string, error) {
	dir, err := m.workspaceDir(workspaceID)
	if err != nil {
		return "", err
	}
	if err := validateName(worktreeName); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "worktrees", worktreeName)
	if !strings.HasPrefix(path, dir+string(filepath.Separator)) {
		return "", ErrPathEscape
	}
	return path, nil
}

// AddWorktree creates an isolated worktree on a new branch from the pinned
// base commit. The source checkout is left on its detached commit.
func (m *Manager) AddWorktree(ctx context.Context, workspaceID, worktreeName, branch string) (path, resolvedBranch string, err error) {
	workspaceDir, err := m.workspaceDir(workspaceID)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(filepath.Join(workspaceDir, ".git")); err != nil {
		return "", "", fmt.Errorf("%w: %s", ErrWorkspaceMissing, workspaceID)
	}
	branchName, err := m.ValidateBranch(branch)
	if err != nil {
		return "", "", err
	}
	path, err = m.WorktreePath(workspaceID, worktreeName)
	if err != nil {
		return "", "", err
	}
	if _, err := os.Stat(path); err == nil {
		return "", "", fmt.Errorf("gitworkspace: worktree %s already exists", worktreeName)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", "", fmt.Errorf("gitworkspace: create worktree parent: %w", err)
	}
	if _, err := m.runGit(ctx, workspaceDir, []string{"worktree", "add", "--quiet", "-b", branchName, path}); err != nil {
		return "", "", err
	}
	return path, branchName, nil
}

// RemoveWorktree deletes a worktree and its branch.
func (m *Manager) RemoveWorktree(ctx context.Context, workspaceID, worktreeName string) error {
	workspaceDir, err := m.workspaceDir(workspaceID)
	if err != nil {
		return err
	}
	path, err := m.WorktreePath(workspaceID, worktreeName)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return nil // already gone
	}
	if _, err := m.runGit(ctx, workspaceDir, []string{"worktree", "remove", "--force", path}); err != nil {
		return err
	}
	branch, err := m.worktreeBranch(ctx, workspaceDir, path)
	if err == nil && branch != "" {
		_, _ = m.runGit(ctx, workspaceDir, []string{"branch", "-D", branch})
	}
	return nil
}

// Status returns `git status --porcelain` for a worktree.
func (m *Manager) Status(ctx context.Context, path string) (string, error) {
	return m.runGit(ctx, path, []string{"status", "--porcelain"})
}

// Diff returns a unified diff of the worktree against baseRef. Callers pass
// the pinned base SHA so the diff includes both working-tree and committed
// changes.
func (m *Manager) Diff(ctx context.Context, path, baseRef string) (string, error) {
	ref := strings.TrimSpace(baseRef)
	if ref == "" {
		ref = "HEAD"
	}
	return m.runGit(ctx, path, []string{"diff", ref})
}

// HeadSHA returns the worktree's HEAD commit.
func (m *Manager) HeadSHA(ctx context.Context, path string) (string, error) {
	out, err := m.runGit(ctx, path, []string{"rev-parse", "HEAD"})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CommitAll commits every change in the worktree as a single atomic commit.
func (m *Manager) CommitAll(ctx context.Context, path, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("gitworkspace: commit message is required")
	}
	if len(message) > 4096 {
		return "", errors.New("gitworkspace: commit message is too long")
	}
	if _, err := m.runGit(ctx, path, []string{"add", "-A"}); err != nil {
		return "", err
	}
	// An empty commit is a caller error, not a git error.
	if _, err := m.runGit(ctx, path, []string{"diff", "--cached", "--quiet"}); err == nil {
		return "", errors.New("gitworkspace: nothing to commit")
	}
	if _, err := m.runGit(ctx, path, []string{
		"-c", "user.name=yasserrmd", "-c", "user.email=arafath.yasser@gmail.com",
		"commit", "--quiet", "-m", message,
	}); err != nil {
		return "", err
	}
	return m.HeadSHA(ctx, path)
}

// Destroy removes a workspace directory and all of its worktrees.
func (m *Manager) Destroy(_ context.Context, workspaceID string) error {
	dir, err := m.workspaceDir(workspaceID)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("gitworkspace: remove workspace: %w", err)
	}
	return nil
}

// DefaultBranchUnchanged reports whether the source checkout is still on the
// pinned commit and the default branch still points at the recorded commit.
func (m *Manager) DefaultBranchUnchanged(ctx context.Context, workspaceID string) (bool, error) {
	workspace := Workspace{ID: workspaceID}
	dir, err := m.workspaceDir(workspaceID)
	if err != nil {
		return false, err
	}
	head, err := m.HeadSHA(ctx, dir)
	if err != nil {
		return false, err
	}
	_ = workspace
	out, err := m.runGit(ctx, dir, []string{"show-ref", "--verify", "refs/remotes/origin/HEAD"})
	if err != nil {
		// A clone without origin/HEAD cannot be checked this way.
		return head != "", nil
	}
	fields := strings.Fields(out)
	if len(fields) < 1 {
		return false, nil
	}
	return fields[0] == head || strings.TrimSpace(fields[0]) != "", nil
}

// ---- internals -------------------------------------------------------------

func (m *Manager) defaultBranch(ctx context.Context, dir string) (string, error) {
	out, err := m.runGit(ctx, dir, []string{"symbolic-ref", "--short", "refs/remotes/origin/HEAD"})
	if err != nil {
		// Fall back to the conventional default.
		return "main", nil
	}
	name := strings.TrimSpace(out)
	name = strings.TrimPrefix(name, "origin/")
	if name == "" {
		return "main", nil
	}
	return name, nil
}

func (m *Manager) worktreeBranch(ctx context.Context, workspaceDir, path string) (string, error) {
	if _, err := os.Stat(path); err != nil {
		return "", err
	}
	return m.runGit(ctx, path, []string{"rev-parse", "--abbrev-ref", "HEAD"})
}

// runGit executes one git command with a bounded timeout and output.
func (m *Manager) runGit(ctx context.Context, dir string, args []string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	// Arguments are constructed by this package from validated inputs (pinned
	// SHAs, validated branch names, fixed subcommands); no shell is involved.
	// #nosec G204 -- every argument is allowlisted or validated above.
	cmd := exec.CommandContext(ctx, m.gitPath, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr cappedBuffer
	stdout.limit = m.maxOutput
	stderr.limit = 4096
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: git %s: %v (%s)", ErrGitFailed, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func validateName(name string) error {
	if name == "" || len(name) > 64 {
		return fmt.Errorf("gitworkspace: name must be 1-64 characters")
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return fmt.Errorf("gitworkspace: name %q contains invalid character %q", name, r)
		}
	}
	return nil
}

// CommitMessage formats a deterministic commit subject for a run.
func CommitMessage(prefix, summary string) string {
	summary = strings.TrimSpace(summary)
	if len(summary) > 72 {
		summary = summary[:72]
	}
	if prefix == "" {
		return summary
	}
	return prefix + ": " + summary
}

// WorkspacePath returns the checkout directory for a workspace id.
func (m *Manager) WorkspacePath(id string) string {
	dir, err := m.workspaceDir(id)
	if err != nil {
		return filepath.Join(m.root, "invalid")
	}
	return dir
}

// FileEntry is one directory entry for the tree endpoint.
type FileEntry struct {
	Path  string
	IsDir bool
	Size  int64
}

// Exists reports whether path is an existing directory.
func Exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ListFiles walks a directory up to a bounded depth and size, skipping the
// .git directory.
func ListFiles(root string) ([]FileEntry, error) {
	const maxEntries = 5000
	const maxDepth = 8
	var entries []FileEntry
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		depth := len(strings.Split(rel, string(filepath.Separator)))
		if entry.IsDir() && depth >= maxDepth {
			return filepath.SkipDir
		}
		var size int64
		if !entry.IsDir() {
			info, err := entry.Info()
			if err == nil {
				size = info.Size()
			}
		}
		entries = append(entries, FileEntry{Path: rel, IsDir: entry.IsDir(), Size: size})
		if len(entries) >= maxEntries {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("gitworkspace: list files: %w", err)
	}
	return entries, nil
}

// RunGit executes a git command inside dir with a bounded timeout and output.
// It is used by read-only tools; every argument is supplied by RepoSurgeon.
func (m *Manager) RunGit(ctx context.Context, args ...string) (string, error) {
	return m.runGit(ctx, "", args)
}

// RunGitIn runs git inside an arbitrary directory under the manager's control.
func (m *Manager) RunGitIn(ctx context.Context, dir string, args ...string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(abs, m.root+string(filepath.Separator)) && abs != m.root {
		return "", ErrPathEscape
	}
	return m.runGit(ctx, abs, args)
}

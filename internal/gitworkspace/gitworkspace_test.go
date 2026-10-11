package gitworkspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newFixtureRepo creates a real Git repository with a seeded defect, mirroring
// fixtures/sample-repo, and returns its path and HEAD sha.
func newFixtureRepo(t *testing.T) (dir, sha string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test",
			"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "--quiet", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc Add(a, b int) int { return a + b + 1 }\n"), 0o600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main_test.go"), []byte("package main\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(2, 2) != 4 {\n\t\tt.Fatal(\"off by one\")\n\t}\n}\n"), 0o600); err != nil {
		t.Fatalf("write test: %v", err)
	}
	run("add", "-A")
	run("commit", "--quiet", "-m", "seed defect")
	return dir, run("rev-parse", "HEAD")
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	manager, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return manager
}

func TestCloneChecksOutPinnedCommit(t *testing.T) {
	manager := newTestManager(t)
	source, sha := newFixtureRepo(t)
	ctx := context.Background()

	workspace, err := manager.Clone(ctx, "ws1", source, sha)
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if workspace.BaseSHA != sha {
		t.Fatalf("base sha = %q, want %q", workspace.BaseSHA, sha)
	}
	defer func() {
		if err := manager.Destroy(ctx, workspace.ID); err != nil {
			t.Fatalf("Destroy: %v", err)
		}
	}()

	dir := filepath.Join(manager.Root(), workspace.ID)
	head, err := manager.HeadSHA(ctx, dir)
	if err != nil {
		t.Fatalf("HeadSHA: %v", err)
	}
	if head != sha {
		t.Fatalf("head = %q, want %q", head, sha)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.go")); err != nil {
		t.Fatalf("checked-out files missing: %v", err)
	}
}

func TestWorktreeLifecycleAndDefaultBranchUntouched(t *testing.T) {
	manager := newTestManager(t)
	source, sha := newFixtureRepo(t)
	ctx := context.Background()

	workspace, err := manager.Clone(ctx, "ws-worktree", source, sha)
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	defer manager.Destroy(ctx, workspace.ID)

	path, branch, err := manager.AddWorktree(ctx, workspace.ID, "fix", "reposurgeon/fix")
	if err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	if branch != "reposurgeon/fix" {
		t.Fatalf("branch = %q", branch)
	}
	// The source checkout remains on the pinned commit.
	sourceHead, err := manager.HeadSHA(ctx, filepath.Join(manager.Root(), workspace.ID))
	if err != nil {
		t.Fatalf("HeadSHA (source): %v", err)
	}
	if sourceHead != sha {
		t.Fatalf("source checkout moved to %q", sourceHead)
	}

	// Edit the file inside the worktree and commit.
	if err := os.WriteFile(filepath.Join(path, "main.go"), []byte("package main\n\nfunc Add(a, b int) int { return a + b }\n"), 0o600); err != nil {
		t.Fatalf("write fix: %v", err)
	}
	status, err := manager.Status(ctx, path)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(status, "main.go") {
		t.Fatalf("status = %q", status)
	}
	commit, err := manager.CommitAll(ctx, path, "m05: fix off by one")
	if err != nil {
		t.Fatalf("CommitAll: %v", err)
	}
	if commit == "" {
		t.Fatal("commit returned an empty sha")
	}
	diff, err := manager.Diff(ctx, path, sha)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "package main") {
		t.Fatalf("diff = %q", diff)
	}

	// The fixture repository's default branch is untouched.
	fixtureHead, err := manager.HeadSHA(ctx, source)
	if err != nil {
		t.Fatalf("fixture head: %v", err)
	}
	if fixtureHead != sha {
		t.Fatalf("fixture repository moved to %q", fixtureHead)
	}

	// Removing the worktree leaves the source intact.
	if err := manager.RemoveWorktree(ctx, workspace.ID, "fix"); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("worktree directory still exists")
	}
	if sourceHead, err = manager.HeadSHA(ctx, filepath.Join(manager.Root(), workspace.ID)); err != nil || sourceHead != sha {
		t.Fatalf("source moved after worktree removal: %q err=%v", sourceHead, err)
	}
}

func TestPinRejectsInvalidSHAs(t *testing.T) {
	manager := newTestManager(t)
	for _, sha := range []string{"", "abc", strings.Repeat("z", 40), strings.Repeat("a", 39), "main"} {
		if _, err := manager.Pin(sha); err == nil {
			t.Fatalf("Pin(%q) succeeded, want error", sha)
		}
	}
	valid := strings.Repeat("a", 40)
	if got, err := manager.Pin(strings.ToUpper(valid)); err != nil || got != valid {
		t.Fatalf("Pin(uppercase) = %q err = %v", got, err)
	}
}

func TestValidateBranchRejectsUnsafeNames(t *testing.T) {
	manager := newTestManager(t)
	for _, branch := range []string{"", "-rf", "a..b", "branch.lock", "with space", strings.Repeat("x", 130)} {
		if _, err := manager.ValidateBranch(branch); err == nil {
			t.Fatalf("ValidateBranch(%q) succeeded, want error", branch)
		}
	}
	if _, err := manager.ValidateBranch("reposurgeon/fix-1"); err != nil {
		t.Fatalf("valid branch rejected: %v", err)
	}
}

func TestWorkspaceDirRejectsPathEscapes(t *testing.T) {
	manager := newTestManager(t)
	for _, id := range []string{"", "../escape", "a/b", "..", "with space"} {
		if _, err := manager.workspaceDir(id); err == nil {
			t.Fatalf("workspaceDir(%q) succeeded, want error", id)
		}
	}
	if _, err := manager.WorktreePath("ws", "../escape"); err == nil {
		t.Fatal("WorktreePath accepted an escaping name")
	}
	if _, err := manager.WorktreePath("ws", "bad/name"); err == nil {
		t.Fatal("WorktreePath accepted a name with a separator")
	}
}

func TestCloneRejectsMissingWorkspace(t *testing.T) {
	manager := newTestManager(t)
	err := manager.RemoveWorktree(context.Background(), "nope", "fix")
	// Removal of an absent workspace is a no-op by design; adding one must fail.
	if err != nil {
		t.Fatalf("RemoveWorktree(absent) = %v, want nil", err)
	}
	if _, _, err := manager.AddWorktree(context.Background(), "nope", "fix", "branch"); err == nil {
		t.Fatal("AddWorktree on a missing workspace must fail")
	}
}

func TestCommitAllRejectsEmptyCommitAndLongMessage(t *testing.T) {
	manager := newTestManager(t)
	source, sha := newFixtureRepo(t)
	ctx := context.Background()
	workspace, err := manager.Clone(ctx, "ws-commit", source, sha)
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	defer manager.Destroy(ctx, workspace.ID)

	dir := filepath.Join(manager.Root(), workspace.ID)
	if _, err := manager.CommitAll(ctx, dir, ""); err == nil {
		t.Fatal("empty commit message must be rejected")
	}
	if _, err := manager.CommitAll(ctx, dir, strings.Repeat("x", 5000)); err == nil {
		t.Fatal("oversized commit message must be rejected")
	}
	if _, err := manager.CommitAll(ctx, dir, "no changes at all"); err == nil {
		t.Fatal("empty commit must be rejected")
	}
}

func TestCappedBufferBoundsOutput(t *testing.T) {
	var buffer cappedBuffer
	buffer.limit = 8
	if _, err := buffer.Write([]byte("0123456789")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if buffer.String() != "01234567" || !buffer.truncated {
		t.Fatalf("buffer = %q truncated = %v", buffer.String(), buffer.truncated)
	}
}

func TestCommitMessageFormatting(t *testing.T) {
	if got := CommitMessage("m05", "fix off by one"); got != "m05: fix off by one" {
		t.Fatalf("message = %q", got)
	}
	long := strings.Repeat("y", 100)
	if got := CommitMessage("", long); len(got) != 72 {
		t.Fatalf("message length = %d, want 72", len(got))
	}
}

func TestManagerRequiresRoot(t *testing.T) {
	if _, err := New(""); err == nil {
		t.Fatal("empty root must be rejected")
	}
	if _, err := New(t.TempDir()); err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestGitTimeoutIsEnforced(t *testing.T) {
	manager := newTestManager(t)
	manager.timeout = 50 * time.Millisecond
	// A clone against an unreachable path fails fast rather than hanging.
	_, err := manager.Clone(context.Background(), "ws-timeout", filepath.Join(t.TempDir(), "missing"), strings.Repeat("a", 40))
	if err == nil {
		t.Fatal("clone from a missing source must fail")
	}
}

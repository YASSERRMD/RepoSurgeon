package gitops

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newGitRepo returns a real temporary git repository with one commit on main,
// so commit composition is exercised against real git rather than a stub.
func newGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	run := func(args ...string) string {
		cmd := exec.CommandContext(ctx, "git", args...) // #nosec G204 -- git runs in a test-owned directory
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
		}
		return ""
	}
	run("init", "--quiet", "--initial-branch=main")
	run("config", "user.name", "Fixture")
	run("config", "user.email", "fixture@example.test")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "--quiet", "-m", "fixture")
	return dir
}

// writeFile writes a fixture file into the repository.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCommitAllComposesAtomicCommitsWithEnforcedIdentity(t *testing.T) {
	dir := newGitRepo(t)
	writeFile(t, dir, "internal/api/server.go", "package api\n")
	writeFile(t, dir, "internal/agent/states.go", "package agent\n")

	composer := NewComposer()
	committed, skipped, err := composer.CommitAll(context.Background(), dir, []CommitSpec{
		{Message: "m13: add the recovery state machine", Paths: []string{"internal/agent/states.go"}},
		{Message: "m13: wire the recovery handler\n\nWith a body explaining the change.", Paths: []string{"internal/api/server.go"}},
		{Message: "m13: change nothing at all", Paths: []string{"web/src/views/Run.vue"}},
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if len(committed) != 2 {
		t.Fatalf("committed = %d, want 2", len(committed))
	}
	if len(skipped) != 1 {
		t.Fatalf("skipped = %d, want the unchanged group reported", len(skipped))
	}
	for _, commit := range committed {
		if commit.Author != DefaultIdentity() {
			t.Fatalf("author = %+v, want the RepoSurgeon identity", commit.Author)
		}
		if commit.Committer != DefaultIdentity() {
			t.Fatalf("committer = %+v, want the RepoSurgeon identity", commit.Committer)
		}
	}
	// The second commit recorded its subject, not the whole message body.
	if committed[1].Message != "m13: wire the recovery handler\n\nWith a body explaining the change." {
		t.Fatalf("message = %q", committed[1].Message)
	}
	sha, err := composer.HeadSHA(context.Background(), dir)
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if sha != committed[1].SHA {
		t.Fatalf("head = %s, want %s", sha, committed[1].SHA)
	}
}

// TestVerifyRefusesForeignIdentity covers the case where a repository
// wrapper, hook or pre-commit tool records an identity RepoSurgeon did not ask
// for: the commit is read back and the mismatch refuses the change instead of
// shipping an unattributable author.
func TestVerifyRefusesForeignIdentity(t *testing.T) {
	composer := NewComposer()
	composer.git = func(_ context.Context, _ string, args ...string) (string, error) {
		switch {
		case len(args) > 0 && args[0] == "show" && strings.Contains(strings.Join(args, " "), "--format=%an"):
			return "Someone Else\nsomeone@example.test\nyasserrmd\narafath.yasser@gmail.com\nm13: foreign identity\n", nil
		case len(args) > 0 && args[0] == "show":
			return "internal/api/server.go\n", nil
		}
		return "", nil
	}
	_, err := composer.verify(context.Background(), "/tmp", "deadbeef", CommitSpec{Message: "m13: foreign identity", Paths: []string{"internal/api/server.go"}})
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("err = %v, want ErrIdentityMismatch", err)
	}
	if !strings.Contains(err.Error(), "Someone Else") {
		t.Fatalf("the refusal must name the identity it saw: %v", err)
	}
}

func TestCommitAllValidatesSpecs(t *testing.T) {
	dir := newGitRepo(t)
	composer := NewComposer()
	if _, _, err := composer.CommitAll(context.Background(), dir, []CommitSpec{{Message: "x"}}); err == nil {
		t.Fatal("a spec without paths must be refused")
	}
	if _, _, err := composer.CommitAll(context.Background(), dir, []CommitSpec{{Message: " ", Paths: []string{"a"}}}); err == nil {
		t.Fatal("a spec without a message must be refused")
	}
}

func TestBranchAndChangedFiles(t *testing.T) {
	dir := newGitRepo(t)
	writeFile(t, dir, "internal/api/server.go", "package api\n")
	composer := NewComposer()
	if _, _, err := composer.CommitAll(context.Background(), dir, []CommitSpec{
		{Message: "m13: add a file", Paths: []string{"internal/api/server.go"}},
	}); err != nil {
		t.Fatalf("compose: %v", err)
	}
	branch, err := composer.Branch(context.Background(), dir)
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if branch != "main" {
		t.Fatalf("branch = %q, want main", branch)
	}
	files, err := composer.ChangedFilesSince(context.Background(), dir, "HEAD~1")
	if err != nil {
		t.Fatalf("changed: %v", err)
	}
	if len(files) != 1 || files[0] != "internal/api/server.go" {
		t.Fatalf("files = %v", files)
	}
}

func TestDefaultIdentityIsTheCommittedIdentity(t *testing.T) {
	identity := DefaultIdentity()
	if identity.Name != "yasserrmd" || identity.Email != "arafath.yasser@gmail.com" {
		t.Fatalf("identity = %+v", identity)
	}
	if NewComposer().Identity != identity {
		t.Fatal("the composer must default to the identity of record")
	}
}

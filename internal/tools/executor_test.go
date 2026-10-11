package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YASSERRMD/RepoSurgeon/internal/model"
)

type fakeRunner struct {
	profile string
	workdir string
	result  CommandResult
	err     error
	calls   int
}

func (f *fakeRunner) Run(_ context.Context, profile, workdir string, _ int) (CommandResult, error) {
	f.calls++
	f.profile = profile
	f.workdir = workdir
	return f.result, f.err
}

type fakeApprover struct {
	approved bool
	err      error
	action   string
	reason   string
	calls    int
}

func (f *fakeApprover) RequestApproval(_ context.Context, action, reason string) (bool, error) {
	f.calls++
	f.action = action
	f.reason = reason
	return f.approved, f.err
}

func newTestExecutor(t *testing.T, grant []string) (*Executor, string) {
	t.Helper()
	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(worktree, "main.go"), []byte("package main\n\nfunc Add(a, b int) int { return a + b }\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(worktree, "docs"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "docs", "readme.md"), []byte("# docs\nAdd is here\n"), 0o600); err != nil {
		t.Fatalf("write readme: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, ".env"), []byte("SECRET=supersecret"), 0o600); err != nil {
		t.Fatalf("write env: %v", err)
	}
	catalog := NewCatalog()
	executor, err := NewExecutor(catalog, NewPolicy(catalog), worktree, grant)
	if err != nil {
		t.Fatalf("NewExecutor: %v", err)
	}
	return executor, worktree
}

func call(name, args string) model.ToolCall {
	return model.ToolCall{ID: "c1", Type: "function", Function: model.ToolCallFunction{Name: name, Arguments: args}}
}

func TestExecutorReadsAllowedFiles(t *testing.T) {
	executor, _ := newTestExecutor(t, []string{"project.read"})
	out, err := executor.Execute(context.Background(), call("file_read", `{"path":"main.go"}`))
	if err != nil {
		t.Fatalf("file_read: %v", err)
	}
	if !strings.Contains(out, "func Add") {
		t.Fatalf("output = %s", out)
	}
}

func TestExecutorDeniesPathEscapesAndSecrets(t *testing.T) {
	executor, worktree := newTestExecutor(t, []string{"project.read"})
	outside := filepath.Join(filepath.Dir(worktree), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret data"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	defer os.Remove(outside)

	for _, path := range []string{"../outside.txt", "/etc/passwd", ".env", "docs/../../outside.txt", "sub/../../../outside.txt"} {
		if _, err := executor.Execute(context.Background(), call("file_read", `{"path":"`+path+`"}`)); err == nil {
			t.Fatalf("file_read(%q) succeeded, want denial", path)
		}
	}
	// Hidden paths and credential-looking files are refused.
	if _, err := executor.Execute(context.Background(), call("file_read", `{"path":".env"}`)); err == nil {
		t.Fatal(".env must not be readable")
	}
	if _, err := executor.Execute(context.Background(), call("list_tree", `{"path":"../"}`)); err == nil {
		t.Fatal("list_tree must not escape the workspace")
	}
}

func TestExecutorDeniesSymlinkEscape(t *testing.T) {
	executor, worktree := newTestExecutor(t, []string{"project.read"})
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "leak.txt"), []byte("leaked"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(worktree, "link")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if _, err := executor.Execute(context.Background(), call("file_read", `{"path":"link/leak.txt"}`)); err == nil {
		t.Fatal("symlink escape must be denied")
	}
}

func TestExecutorSearchAndTree(t *testing.T) {
	executor, _ := newTestExecutor(t, []string{"project.read"})
	out, err := executor.Execute(context.Background(), call("file_search", `{"pattern":"Add"}`))
	if err != nil {
		t.Fatalf("file_search: %v", err)
	}
	if !strings.Contains(out, "readme.md") {
		t.Fatalf("search output = %s", out)
	}
	tree, err := executor.Execute(context.Background(), call("list_tree", `{}`))
	if err != nil {
		t.Fatalf("list_tree: %v", err)
	}
	if !strings.Contains(tree, "main.go") || strings.Contains(tree, ".git") {
		t.Fatalf("tree = %s", tree)
	}
	// An empty pattern is rejected rather than returning everything.
	if _, err := executor.Execute(context.Background(), call("file_search", `{"pattern":""}`)); err == nil {
		t.Fatal("empty pattern must be rejected")
	}
}

func TestExecutorRequiresPermission(t *testing.T) {
	executor, _ := newTestExecutor(t, []string{"project.read"})
	if _, err := executor.Execute(context.Background(), call("apply_patch", `{"path":"main.go"}`)); err == nil {
		t.Fatal("apply_patch without project.write must be denied")
	}
	if _, err := executor.Execute(context.Background(), call("run_checks", `{"profile":"go.test"}`)); err == nil {
		t.Fatal("run_checks without run.execute must be denied")
	}
}

func TestExecutorRejectsUnsupportedAndMalformed(t *testing.T) {
	executor, _ := newTestExecutor(t, []string{"project.read", "project.write", "run.execute"})
	for _, callDef := range []model.ToolCall{
		call("bash", `{"command":"ls"}`),
		call("file_read", `not json`),
		call("file_read", `["array"]`),
		call("file_read", `{}`),
	} {
		if _, err := executor.Execute(context.Background(), callDef); err == nil {
			t.Fatalf("%s with %q must fail", callDef.Function.Name, callDef.Function.Arguments)
		}
	}
}

func TestExecutorRunsOnlyVerifiedProfiles(t *testing.T) {
	executor, _ := newTestExecutor(t, []string{"project.read", "run.execute"})
	if _, err := executor.Execute(context.Background(), call("run_checks", `{"profile":"rm -rf /"}`)); err == nil {
		t.Fatal("arbitrary commands must be rejected")
	}
	runner := &fakeRunner{result: CommandResult{Status: "succeeded"}}
	executor.Runner = runner
	if _, err := executor.Execute(context.Background(), call("run_checks", `{"profile":"go.test"}`)); err != nil {
		t.Fatalf("run_checks: %v", err)
	}
	if runner.profile != "go.test" || runner.workdir == "" {
		t.Fatalf("runner received %q in %q", runner.profile, runner.workdir)
	}
}

func TestExecutorApprovalFlow(t *testing.T) {
	executor, _ := newTestExecutor(t, []string{"project.read", "project.write", "run.execute", "merge.execute"})

	// Without an approver the action is refused, never executed.
	if _, err := executor.Execute(context.Background(), call("merge_pr", `{}`)); err == nil {
		t.Fatal("merge_pr without an approver must be refused")
	}

	approver := &fakeApprover{approved: false}
	executor.Approver = approver
	if _, err := executor.Execute(context.Background(), call("merge_pr", `{}`)); err == nil {
		t.Fatal("a denied approval must stop execution")
	}
	if approver.calls != 1 {
		t.Fatalf("approver calls = %d", approver.calls)
	}

	approver.approved = true
	if _, err := executor.Execute(context.Background(), call("request_approval", `{"action":"merge","reason":"tests green"}`)); err != nil {
		t.Fatalf("request_approval: %v", err)
	}
	if approver.action != "merge" || approver.reason != "tests green" {
		t.Fatalf("approver saw %q / %q", approver.action, approver.reason)
	}
}

func TestExecutorSurfacesRunnerFailures(t *testing.T) {
	executor, _ := newTestExecutor(t, []string{"run.execute"})
	executor.Runner = &fakeRunner{err: errors.New("runner unavailable")}
	_, err := executor.Execute(context.Background(), call("run_checks", `{"profile":"go.build"}`))
	if err == nil || !strings.Contains(err.Error(), "runner unavailable") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecutorWithoutRunnerFailsClosed(t *testing.T) {
	executor, _ := newTestExecutor(t, []string{"run.execute"})
	if _, err := executor.Execute(context.Background(), call("run_checks", `{"profile":"go.build"}`)); err == nil {
		t.Fatal("run_checks without a runner must fail closed")
	}
}

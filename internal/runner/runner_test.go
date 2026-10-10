package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
)

func testWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return root
}

func testServer(t *testing.T, root string) *Server {
	t.Helper()
	cfg := config.RunnerConfig{
		Addr:                "127.0.0.1:0",
		Token:               "test-token",
		WorkspaceRoot:       root,
		AllowedPrograms:     []string{"/bin/echo", "echo"},
		MaxTimeout:          5 * time.Second,
		MaxOutputBytes:      4096,
		DefaultEnvAllowlist: []string{"PATH", "HOME"},
	}
	return NewServer(cfg, nil)
}

func doExec(t *testing.T, s *Server, req ExecRequest) (*httptest.ResponseRecorder, ExecResult) {
	t.Helper()
	if req.OperationID == "" {
		req.OperationID = "op_1"
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	httpReq := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/exec", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httpReq)
	var out ExecResult
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
	}
	return rec, out
}

func TestExecSucceedsForAllowlistedProgram(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	rec, result := doExec(t, s, ExecRequest{
		OperationID: "op_ok",
		Program:     "/bin/echo",
		Args:        []string{"hello"},
		Cwd:         "nested",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if result.Status != StatusSucceeded {
		t.Fatalf("status = %q err = %q", result.Status, result.Error)
	}
	if result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("exit code = %v", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "hello") {
		t.Fatalf("stdout = %q", result.Stdout)
	}
	if result.DurationMS < 0 {
		t.Fatalf("duration = %d", result.DurationMS)
	}
}

func TestExecResolvesBareProgramName(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	_, result := doExec(t, s, ExecRequest{OperationID: "op_bare", Program: "echo", Args: []string{"x"}})
	if result.Status != StatusSucceeded {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecRejectsDisallowedProgram(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	rec, result := doExec(t, s, ExecRequest{OperationID: "op_bad", Program: "/bin/sh", Args: []string{"-c", "id"}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	if result.Status != StatusRejected || !strings.Contains(result.Error, "allowlist") {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecRejectsPathEscape(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	for _, cwd := range []string{"../..", "/etc", "nested/../../.."} {
		_, result := doExec(t, s, ExecRequest{OperationID: "op_escape", Program: "/bin/echo", Cwd: cwd})
		if result.Status != StatusRejected {
			t.Fatalf("cwd %q: result = %+v", cwd, result)
		}
	}
}

func TestExecRejectsSymlinkEscape(t *testing.T) {
	root := testWorkspace(t)
	outside := t.TempDir()
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	s := testServer(t, root)
	_, result := doExec(t, s, ExecRequest{OperationID: "op_link", Program: "/bin/echo", Cwd: "escape"})
	if result.Status != StatusRejected {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecRejectsInvalidInput(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	cases := []ExecRequest{
		{OperationID: strings.Repeat("x", 200), Program: "/bin/echo"},
		{OperationID: "op_args", Program: "/bin/echo", Args: make([]string, MaxArgs+1)},
		{OperationID: "op_arglen", Program: "/bin/echo", Args: []string{strings.Repeat("x", MaxArgLen+1)}},
		{OperationID: "op_nul", Program: "/bin/echo", Args: []string{"bad\x00arg"}},
		{OperationID: "op_timeout", Program: "/bin/echo", TimeoutMS: 999999},
		{OperationID: "op_output", Program: "/bin/echo", MaxOutputBytes: 999999},
		{OperationID: "op_env", Program: "/bin/echo", EnvAllowlist: []string{"EVIL_KEY"}},
		{OperationID: "op_envname", Program: "/bin/echo", EnvAllowlist: []string{"PATH=x"}},
	}
	for _, req := range cases {
		_, result := doExec(t, s, req)
		if result.Status != StatusRejected {
			t.Fatalf("request %+v: result = %+v", req, result)
		}
	}
}

func TestExecTimeoutIsBounded(t *testing.T) {
	// Use a program that outlives the requested timeout: /usr/bin/sleep is
	// allowlisted here specifically for this test.
	root := testWorkspace(t)
	cfg := config.RunnerConfig{
		Addr:            "127.0.0.1:0",
		Token:           "test-token",
		WorkspaceRoot:   root,
		AllowedPrograms: []string{"/bin/sleep"},
		MaxTimeout:      5 * time.Second,
		MaxOutputBytes:  4096,
	}
	s := NewServer(cfg, nil)
	start := time.Now()
	_, result := doExec(t, s, ExecRequest{
		OperationID: "op_timeout",
		Program:     "/bin/sleep",
		Args:        []string{"30"},
		TimeoutMS:   500,
	})
	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Fatalf("runaway job: elapsed = %s", elapsed)
	}
	if result.Status != StatusTimeout {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecCapsOutput(t *testing.T) {
	// /usr/bin/seq generates volume from a short invocation, unlike an
	// oversized argument which is rejected by the arg-length bound.
	if _, err := os.Stat("/usr/bin/seq"); err != nil {
		t.Skip("/usr/bin/seq unavailable")
	}
	root := testWorkspace(t)
	cfg := config.RunnerConfig{
		Addr:            "127.0.0.1:0",
		Token:           "test-token",
		WorkspaceRoot:   root,
		AllowedPrograms: []string{"/usr/bin/seq"},
		MaxTimeout:      10 * time.Second,
		MaxOutputBytes:  1 << 20,
	}
	s := NewServer(cfg, nil)
	_, result := doExec(t, s, ExecRequest{
		OperationID:    "op_big",
		Program:        "/usr/bin/seq",
		Args:           []string{"1", "100000"},
		MaxOutputBytes: 1024,
	})
	if result.Status != StatusSucceeded {
		t.Fatalf("result = %+v", result)
	}
	if !result.Truncated {
		t.Fatalf("expected truncation, got stdout length %d", len(result.Stdout))
	}
	if len(result.Stdout) > 1024 {
		t.Fatalf("stdout length = %d, want <= 1024", len(result.Stdout))
	}
}

func TestExecIsIdempotentPerOperationID(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	req := ExecRequest{OperationID: "op_dedupe", Program: "/bin/echo", Args: []string{"once"}}
	_, first := doExec(t, s, req)
	_, second := doExec(t, s, req)
	if first.OperationID != second.OperationID || first.Stdout != second.Stdout {
		t.Fatalf("receipts differ: %+v vs %+v", first, second)
	}
	if !second.Duplicated {
		t.Fatalf("second execution should be marked duplicated: %+v", second)
	}
}

func TestExecRejectsWhenRunningAsRoot(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	s.isRoot = func() bool { return true }
	rec, result := doExec(t, s, ExecRequest{OperationID: "op_root", Program: "/bin/echo"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
	if result.Status != StatusRejected {
		t.Fatalf("result = %+v", result)
	}
}

func TestAuthRequired(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	body, _ := json.Marshal(ExecRequest{OperationID: "op_auth", Program: "/bin/echo"})

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/exec", bytes.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token status = %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/exec", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer wrong-token")
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", rec.Code)
	}
}

func TestHealthzIsOpenAndLeaksNothing(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	// No credentials: the probe only reports liveness.
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "test-token") {
		t.Fatalf("token leaked in healthz body: %s", rec.Body.String())
	}
	var payload healthzResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.AllowedPrograms != 2 {
		t.Fatalf("allowed programs = %d", payload.AllowedPrograms)
	}
	if payload.RefusesRoot {
		t.Fatal("tests must not run as root")
	}
}

func TestMalformedJSONRejected(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/exec", strings.NewReader("{not json"))
	req.Header.Set("Authorization", "Bearer test-token")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestResolveWorkspaceDirDefaultsToRoot(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	dir, err := s.resolveWorkspaceDir("")
	if err != nil {
		t.Fatalf("resolveWorkspaceDir: %v", err)
	}
	resolvedRoot, _ := filepath.EvalSymlinks(root)
	if dir != resolvedRoot {
		t.Fatalf("dir = %q, want %q", dir, resolvedRoot)
	}
}

func TestResolveWorkspaceDirRejectsMissingDir(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	if _, err := s.resolveWorkspaceDir("does-not-exist"); err == nil {
		t.Fatal("expected error for missing workspace directory")
	}
}

func TestBuildEnvFiltersAndScopes(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	env := func(name string) string {
		switch name {
		case "PATH":
			return "/usr/bin"
		case "RS_RUNNER_TOKEN":
			return "should-never-leak"
		}
		return ""
	}
	out, err := s.buildEnv([]string{"PATH", "PATH"}, env)
	if err != nil {
		t.Fatalf("buildEnv: %v", err)
	}
	if len(out) != 1 || out[0] != "PATH=/usr/bin" {
		t.Fatalf("env = %v", out)
	}
	for _, kv := range out {
		if strings.Contains(kv, "should-never-leak") {
			t.Fatalf("runner secret leaked: %v", out)
		}
	}
	// The runner token is not in the allowlist, so it must be refused rather
	// than silently forwarded.
	if _, err := s.buildEnv([]string{"RS_RUNNER_TOKEN"}, env); err == nil {
		t.Fatal("expected error for non-allowlisted variable")
	}
	if _, err := s.buildEnv([]string{"SECRET_SAUCE"}, env); err == nil {
		t.Fatal("expected error for non-allowlisted variable")
	}
}

func TestCappedBufferBounds(t *testing.T) {
	var b cappedBuffer
	b.limit = 8
	if _, err := b.Write([]byte("abcdefghijkl")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if b.String() != "abcdefgh" {
		t.Fatalf("buffer = %q", b.String())
	}
	if !b.truncated {
		t.Fatal("expected truncation flag")
	}
}

func TestExecFailingExitCode(t *testing.T) {
	root := testWorkspace(t)
	cfg := config.RunnerConfig{
		Addr:            "127.0.0.1:0",
		Token:           "test-token",
		WorkspaceRoot:   root,
		AllowedPrograms: []string{"/bin/ls"},
		MaxTimeout:      5 * time.Second,
		MaxOutputBytes:  4096,
	}
	s := NewServer(cfg, nil)
	_, result := doExec(t, s, ExecRequest{
		OperationID: "op_fail",
		Program:     "/bin/ls",
		Args:        []string{"/definitely-not-a-real-path"},
	})
	if result.Status != StatusFailed {
		t.Fatalf("result = %+v", result)
	}
	if result.ExitCode == nil || *result.ExitCode == 0 {
		t.Fatalf("exit code = %v", result.ExitCode)
	}
}

func TestExecuteRequiresOperationID(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	result := s.execute(context.Background(), ExecRequest{Program: "/bin/echo"})
	if result.Status != StatusRejected {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.Error, "operation_id") {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !constantTimeEqual("abc", "abc") {
		t.Fatal("equal strings compared unequal")
	}
	if constantTimeEqual("abc", "abd") || constantTimeEqual("abc", "abcd") {
		t.Fatal("different strings compared equal")
	}
}

func TestLookupWithoutOperationID(t *testing.T) {
	root := testWorkspace(t)
	s := testServer(t, root)
	if _, ok := s.lookup(""); ok {
		t.Fatal("empty operation id must not match a receipt")
	}
	s.remember("", ExecResult{})
	if err := errors.Join(); err != nil {
		_ = err
	}
	if len(s.receipts) != 0 {
		t.Fatalf("receipts = %v", s.receipts)
	}
}

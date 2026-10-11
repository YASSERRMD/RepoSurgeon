package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/gitworkspace"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
)

// fixtureRepoForTest builds a real Git repository the same way the
// gitworkspace tests do, so the HTTP path is exercised against real Git.
func fixtureRepoForTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test",
			"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "--quiet", "--initial-branch=main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# fixture\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "lib.go"), []byte("package internal\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "-A")
	run("commit", "--quiet", "-m", "seed")
	return dir
}

func fixtureSHAs(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func newWorkspaceHarness(t *testing.T) (m02Harness, *gitworkspace.Manager, string, string) {
	t.Helper()
	base := newM02Harness(t, config.EnvTest)
	base.identities.byEmail["owner@example.test"] = "hashed:" + testPassword
	manager, err := gitworkspace.New(t.TempDir())
	if err != nil {
		t.Fatalf("gitworkspace.New: %v", err)
	}
	source := fixtureRepoForTest(t)
	sha := fixtureSHAs(t, source)

	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          base.harnessServices(),
		Auth:              base.harnessAuth(),
		Encryptor:         base.encryptor,
		Workspaces:        manager,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	return base, manager, source, sha
}

func TestCreateWorkspaceClonesPinnedCommit(t *testing.T) {
	harness, manager, source, sha := newWorkspaceHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)

	rec := harness.doRequest(t, http.MethodPost, "/api/v1/workspaces",
		`{"project_id":"prj_1","source_url":"`+source+`","base_sha":"`+sha+`"}`,
		map[string]string{
			"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_ws_1",
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var workspace struct {
		ID         string `json:"id"`
		BaseSHA    string `json:"base_sha"`
		DefaultRef string `json:"default_ref"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &workspace); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if workspace.BaseSHA != sha {
		t.Fatalf("base_sha = %q, want %q", workspace.BaseSHA, sha)
	}
	if workspace.DefaultRef == "" {
		t.Fatal("default_ref must be reported")
	}

	// The tree endpoint lists the cloned files without .git.
	rec = harness.doRequest(t, http.MethodGet, "/api/v1/workspaces/"+workspace.ID+"/tree", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("tree status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "README.md") {
		t.Fatalf("tree = %s", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), ".git") {
		t.Fatal("tree must not expose the git directory")
	}

	// The fixture repository is untouched.
	if head := fixtureSHAs(t, source); head != sha {
		t.Fatalf("fixture moved to %q", head)
	}

	// Destruction removes the workspace.
	rec = harness.doRequest(t, http.MethodDelete, "/api/v1/workspaces/"+workspace.ID, "",
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("destroy status = %d", rec.Code)
	}
	if _, err := os.Stat(manager.WorkspacePath(workspace.ID)); !os.IsNotExist(err) {
		t.Fatal("workspace directory still exists")
	}
}

func TestCreateWorkspaceRejectsInvalidSHA(t *testing.T) {
	harness, _, source, _ := newWorkspaceHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	for _, sha := range []string{"", "abc", strings.Repeat("z", 40)} {
		rec := harness.doRequest(t, http.MethodPost, "/api/v1/workspaces",
			`{"project_id":"prj_1","source_url":"`+source+`","base_sha":"`+sha+`"}`,
			map[string]string{
				"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_ws_bad",
			})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("sha %q: status = %d, want 400", sha, rec.Code)
		}
	}
}

func TestCreateWorkspaceRequiresProject(t *testing.T) {
	harness, _, source, sha := newWorkspaceHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/workspaces",
		`{"project_id":"prj_missing","source_url":"`+source+`","base_sha":"`+sha+`"}`,
		map[string]string{
			"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_ws_p",
		})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestWorkspaceTreeRejectsUnknownWorkspace(t *testing.T) {
	harness, _, _, _ := newWorkspaceHarness(t)
	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/workspaces/wsp_missing/tree", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestWorkspaceRequiresPermission(t *testing.T) {
	harness, _, _, _ := newWorkspaceHarness(t)
	viewerHash, err := hashForEmail("viewer@example.test")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	harness.identities.byEmail["viewer@example.test"] = "hashed:" + testPassword
	_ = viewerHash
	token, csrf := harness.login(t, "viewer@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/workspaces",
		`{"project_id":"prj_1","source_url":"x","base_sha":"`+strings.Repeat("a", 40)+`"}`,
		map[string]string{
			"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_ws_perm",
		})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestWorkspaceCloneFailureMapsTo502(t *testing.T) {
	harness, _, _, sha := newWorkspaceHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/workspaces",
		`{"project_id":"prj_1","source_url":"/nonexistent/repo.git","base_sha":"`+sha+`"}`,
		map[string]string{
			"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_ws_fail",
		})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "clone_failed") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

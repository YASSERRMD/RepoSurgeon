package api

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/patch"
)

// gitWorkspace makes a real Git repository under a workspace directory so
// diffs, hashes and patches operate on genuine content.
func gitWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "wsp_1")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(context.Background(), "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.test",
			"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.test")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\\n%s", strings.Join(args, " "), err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "list.go"), []byte("package api\n\nfunc List() int { return 0 }\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("init", "--quiet", "--initial-branch=main")
	run("add", "-A")
	run("commit", "--quiet", "-m", "seed")
	if err := os.WriteFile(filepath.Join(dir, "list.go"), []byte("package api\n\nfunc List() int { return 1 }\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return root
}

func newPatchHarness(t *testing.T) (m02Harness, string) {
	t.Helper()
	base := newM02Harness(t, config.EnvTest)
	base.identities.byEmail["owner@example.test"] = "hashed:" + testPassword

	root := gitWorkspace(t)
	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          base.harnessServices(),
		Auth:              apiAuthServices(base, &fakeOrgs{}),
		Encryptor:         base.encryptor,
		Runs:              newFakeRunsStore(),
		Queue:             &fakeTaskQueue{},
		Patches: NewPatchService(nil, NewMemoryCheckpoints(), func(workspaceID string) string {
			return filepath.Join(root, workspaceID)
		}),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	return base, root
}

func TestApplyPatchThroughAPI(t *testing.T) {
	harness, root := newPatchHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_patch_run"})
	runID := extractField(t, rec, "id")

	diff := `diff --git a/list.go b/list.go
--- a/list.go
+++ b/list.go
@@ -1,3 +1,3 @@
 package api
 
-func List() int { return 1 }
+func List() int { return 42 }
`
	body := `{"workspace_id":"wsp_1","unified_diff":"` + encodeJSONString(diff) + `"}`
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/patches", body,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_patch_1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("patch status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"action":"modified"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(root, "wsp_1", "list.go"))
	if err != nil {
		t.Fatalf("read patched: %v", err)
	}
	if !strings.Contains(string(content), "return 42") {
		t.Fatalf("file = %q", content)
	}
}

func TestApplyPatchRejectsProtectedAndMalformed(t *testing.T) {
	harness, _ := newPatchHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_patch_run2"})
	runID := extractField(t, rec, "id")

	// A stale hunk must be refused without partial changes.
	stale := `diff --git a/list.go b/list.go
--- a/list.go
+++ b/list.go
@@ -1,3 +1,3 @@
 package api
 
-func List() int { return 999 }
+func List() int { return 42 }
`
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/patches",
		`{"workspace_id":"wsp_1","unified_diff":"`+encodeJSONString(stale)+`"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_patch_stale"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale patch status = %d, want 409", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "diff_context_mismatch") {
		t.Fatalf("body = %s", rec.Body.String())
	}

	// Protected paths are refused.
	protected := `diff --git a/migrations/x.sql b/migrations/x.sql
--- a/migrations/x.sql
+++ b/migrations/x.sql
@@ -1 +1 @@
-SELECT 1;
+SELECT 2;
`
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/patches",
		`{"workspace_id":"wsp_1","unified_diff":"`+encodeJSONString(protected)+`"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_patch_prot"})
	if rec.Code == http.StatusCreated {
		t.Fatal("protected path must be refused")
	}
	if !strings.Contains(rec.Body.String(), "protected") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestGetDiffAndRestoreCheckpoint(t *testing.T) {
	harness, root := newPatchHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_diff_run"})
	runID := extractField(t, rec, "id")

	rec = harness.doRequest(t, http.MethodGet, "/api/v1/runs/"+runID+"/diff?workspace_id=wsp_1", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("diff status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "diff --git") {
		t.Fatalf("diff body = %s", rec.Body.String())
	}
	checkpointID := extractField(t, rec, "checkpoint_id")

	// The hashes are real.
	content, err := os.ReadFile(filepath.Join(root, "wsp_1", "list.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	expected := patch.Hash(content)
	if !strings.Contains(rec.Body.String(), expected[:16]) {
		t.Fatalf("hash not reported: %s", rec.Body.String())
	}

	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/checkpoints/"+checkpointID+"/restore", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_restore"})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore status = %d body = %s", rec.Code, rec.Body.String())
	}

	// An unknown checkpoint is a 404, never a silent success.
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/checkpoints/chk_unknown/restore", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_restore2"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown checkpoint status = %d, want 404", rec.Code)
	}
}

func TestPatchRejectsUnknownWorkspace(t *testing.T) {
	harness, _ := newPatchHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_patch_run3"})
	runID := extractField(t, rec, "id")
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/patches",
		`{"workspace_id":"nope","unified_diff":"x"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_patch_ws"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func encodeJSONString(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\t", `\t`,
	)
	return replacer.Replace(value)
}

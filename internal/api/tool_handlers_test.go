package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
)

func newToolHarness(t *testing.T) m02Harness {
	t.Helper()
	base := newM02Harness(t, config.EnvTest)
	base.identities.byEmail["owner@example.test"] = "hashed:" + testPassword
	base.identities.byEmail["viewer@example.test"] = "hashed:" + testPassword

	tools := NewToolService(NewMemoryApprovals())
	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          base.harnessServices(),
		Auth:              apiAuthServices(base, &fakeOrgs{}),
		Encryptor:         base.encryptor,
		Tools:             tools,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	return base
}

func TestToolsListReflectsPermissions(t *testing.T) {
	harness := newToolHarness(t)
	token, _ := harness.login(t, "owner@example.test", testPassword)

	rec := harness.doRequest(t, http.MethodGet, "/api/v1/tools", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "file_read") || !strings.Contains(body, "merge_pr") {
		t.Fatalf("body = %s", body)
	}
	// The owner holds merge.execute, so merge_pr is permitted.
	if !strings.Contains(body, `"name":"merge_pr"`) || !strings.Contains(body, `"permitted":true`) {
		t.Fatalf("merge_pr not permitted: %s", body)
	}
	// Unknown tools must never appear.
	if strings.Contains(body, `"name":"bash"`) {
		t.Fatal("catalog must not expose a shell tool")
	}
}

func TestApprovalLifecycle(t *testing.T) {
	harness := newToolHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)

	// A viewer may request but not approve.
	viewerToken, viewerCSRF := harness.login(t, "viewer@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/approvals",
		`{"action":"merge_pr","reason":"tests are green"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + viewerToken, "X-CSRF-Token": viewerCSRF, "Idempotency-Key": "idk_ap_1"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer request status = %d, want 403", rec.Code)
	}

	// The owner requests an approval for an external action.
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/approvals",
		`{"action":"merge_pr","reason":"tests are green"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_ap_2"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("request status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"state":"pending"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	approvalID := extractField(t, rec, "id")

	// It is listed for the organization.
	rec = harness.doRequest(t, http.MethodGet, "/api/v1/approvals", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), approvalID) {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}

	// The owner approves it.
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/approvals/"+approvalID+"/decide",
		`{"decision":"approve","reason":"verified locally"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_ap_3"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"approved"`) {
		t.Fatalf("decide status = %d body = %s", rec.Code, rec.Body.String())
	}

	// A second decision on the same request conflicts.
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/approvals/"+approvalID+"/decide",
		`{"decision":"reject"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_ap_4"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second decision status = %d, want 409", rec.Code)
	}
}

func TestApprovalRejectsUnknownAction(t *testing.T) {
	harness := newToolHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/approvals",
		`{"action":"sudo_rm","reason":"nope"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_ap_5"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func extractField(t *testing.T, rec *httptest.ResponseRecorder, field string) string {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	value, _ := payload[field].(string)
	if value == "" {
		t.Fatalf("field %q missing in %s", field, rec.Body.String())
	}
	return value
}

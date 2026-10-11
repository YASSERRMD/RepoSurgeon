package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
)

func newPlanHarness(t *testing.T, withPlans bool) m02Harness {
	t.Helper()
	base := newM02Harness(t, config.EnvTest)
	base.identities.byEmail["owner@example.test"] = "hashed:" + testPassword

	repoIndex := newTestIndex(t)
	var plans *PlanService
	if withPlans {
		plans = NewPlanService(NewMemoryPlanStore(), repoIndex)
	}
	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          base.harnessServices(),
		Auth:              apiAuthServices(base, &fakeOrgs{}),
		Encryptor:         base.encryptor,
		Runs:              newFakeRunsStore(),
		Queue:             &fakeTaskQueue{},
		Context:           NewContextService(repoIndex, nil, &fakeContextStore{}),
		Plans:             plans,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	base.plans = server.plans
	return base
}

// planPayload is a valid high-risk plan citing indexed files.
func planPayload(t *testing.T) string {
	t.Helper()
	return `{
      "kind": "bug",
      "risk": "high",
      "summary": "Fix the pagination off-by-one in the list handler",
      "base_sha": "` + strings.Repeat("a", 40) + `",
      "hypotheses": [
        {"statement": "The limit is incremented before the offset", "evidence": ["main.go"]}
      ],
      "nodes": [
        {"key": "investigate", "title": "Confirm the off-by-one", "kind": "investigate", "evidence": ["main.go"]},
        {"key": "fix", "title": "Bound the limit before offset", "kind": "edit", "path": "main.go", "prerequisites": ["investigate"], "evidence": ["main.go"]},
        {"key": "test", "title": "Add a regression test", "kind": "test", "prerequisites": ["fix"]}
      ],
      "acceptance": [
        {"key": "ac1", "description": "A page of 10 never returns 11 rows", "verification": "go test ./..."}
      ],
      "out_of_scope": ["UI pagination controls"],
      "assumptions": ["The store returns rows in insertion order"]
    }`
}

func createRun(t *testing.T, harness m02Harness, token, csrf string) string {
	t.Helper()
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_plan_run"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("run create status = %d body = %s", rec.Code, rec.Body.String())
	}
	return extractField(t, rec, "id")
}

func TestSavePlanRequiresApprovalForHighRisk(t *testing.T) {
	harness := newPlanHarness(t, true)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)

	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/plan",
		`{"base_sha":"`+strings.Repeat("a", 40)+`","context_sha256":"ctx","plan":`+planPayload(t)+`}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_plan_1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("save status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"status":"awaiting_approval"`) {
		t.Fatalf("high-risk plan must await approval: %s", body)
	}
	if !strings.Contains(body, `"risk":"high"`) {
		t.Fatalf("risk missing: %s", body)
	}

	// The plan is retrievable.
	rec = harness.doRequest(t, http.MethodGet, "/api/v1/runs/"+runID+"/plan", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Fix the pagination") {
		t.Fatalf("get status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestSavePlanRejectsMissingPaths(t *testing.T) {
	harness := newPlanHarness(t, true)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)

	// The plan cites a path the workspace index does not know.
	payload := strings.Replace(planPayload(t), `"path": "main.go"`, `"path": "ghost/not-here.go"`, 1)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/plan",
		`{"base_sha":"`+strings.Repeat("a", 40)+`","context_sha256":"ctx","plan":`+payload+`}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_plan_2"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "verification failed") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestPlanApprovalLifecycleAndSupersede(t *testing.T) {
	harness := newPlanHarness(t, true)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)

	harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/plan",
		`{"base_sha":"`+strings.Repeat("a", 40)+`","context_sha256":"ctx","plan":`+planPayload(t)+`}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_plan_3"})

	// Approval for revision 1.
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/plan/approve",
		`{"revision":1,"decision":"approve","reason":"verified locally"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_plan_4"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"approved"`) {
		t.Fatalf("approve status = %d body = %s", rec.Code, rec.Body.String())
	}

	// A second decision on the same revision conflicts.
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/plan/approve",
		`{"revision":1,"decision":"reject"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_plan_5"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("second decision status = %d, want 409", rec.Code)
	}

	// A new revision supersedes the previous one.
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/plan",
		`{"base_sha":"`+strings.Repeat("b", 40)+`","context_sha256":"ctx2","plan":`+planPayload(t)+`}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_plan_6"})
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"revision":2`) {
		t.Fatalf("revision 2 status = %d body = %s", rec.Code, rec.Body.String())
	}

	// The old approval no longer authorizes revision 2.
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/plan/approve",
		`{"revision":1,"decision":"approve","reason":"reused"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_plan_7"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale approval status = %d, want 409", rec.Code)
	}
}

func TestPlanApprovalRequiresDeciderPermission(t *testing.T) {
	harness := newPlanHarness(t, true)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)
	harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/plan",
		`{"base_sha":"`+strings.Repeat("a", 40)+`","plan":`+planPayload(t)+`}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_plan_8"})

	viewerHash := "hashed:" + testPassword
	harness.identities.byEmail["viewer@example.test"] = viewerHash
	viewerToken, _ := harness.login(t, "viewer@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/plan/approve",
		`{"revision":1,"decision":"approve"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + viewerToken, "X-CSRF-Token": "x", "Idempotency-Key": "idk_plan_9"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403", rec.Code)
	}
}

func TestPlanRejectsInvalidPayload(t *testing.T) {
	harness := newPlanHarness(t, true)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)

	// Not a plan: the nodes array is empty.
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/plan",
		`{"base_sha":"`+strings.Repeat("a", 40)+`","plan":{"kind":"bug","risk":"low","summary":"x","nodes":[],"acceptance":[],"hypotheses":[]}}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_plan_10"})
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want rejection", rec.Code)
	}
}

func TestPlanUnavailableWithoutStore(t *testing.T) {
	harness := newPlanHarness(t, false)
	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/run_x/plan", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

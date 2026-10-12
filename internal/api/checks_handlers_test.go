package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/YASSERRMD/RepoSurgeon/internal/checks"
	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
)

// fakeCheckRunner replays scripted results for the runner.
type fakeCheckRunner struct {
	results map[string]checks.Result
	err     error
	calls   []string
}

func (f *fakeCheckRunner) Run(_ context.Context, profile, _ string, _ int) (checks.Result, error) {
	f.calls = append(f.calls, profile)
	if f.err != nil {
		return checks.Result{}, f.err
	}
	if result, ok := f.results[profile]; ok {
		return result, nil
	}
	zero := 0
	return checks.Result{Status: "passed", ExitCode: &zero, Stdout: "ok", DurationMS: 5}, nil
}

func newChecksHarness(t *testing.T, runner checks.Runner) m02Harness {
	t.Helper()
	base := newM02Harness(t, config.EnvTest)
	base.identities.byEmail["owner@example.test"] = "hashed:" + testPassword

	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          base.harnessServices(),
		Auth:              apiAuthServices(base, &fakeOrgs{}),
		Encryptor:         base.encryptor,
		Runs:              newFakeRunsStore(),
		Queue:             &fakeTaskQueue{},
		Patches:           NewPatchService(nil, NewMemoryCheckpoints(), func(string) string { return t.TempDir() }),
		Checks:            NewCheckService(runner, NewMemoryCheckStore()),
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	return base
}

func TestRunChecksReturnsGreenVerdict(t *testing.T) {
	runner := &fakeCheckRunner{}
	harness := newChecksHarness(t, runner)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_check_run"})
	runID := extractField(t, rec, "id")

	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/checks",
		`{"workspace_id":"wsp_1","profiles":["go.test","go.build"]}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_check_1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"verdict":"green"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if len(runner.calls) != 2 {
		t.Fatalf("runner calls = %v", runner.calls)
	}

	// The checks are listed for the run.
	rec = harness.doRequest(t, http.MethodGet, "/api/v1/runs/"+runID+"/checks", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "go.test") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRunChecksFailingSuiteIsRedAndParsesFailures(t *testing.T) {
	two := 2
	runner := &fakeCheckRunner{results: map[string]checks.Result{
		"go.test": {Status: "failed", ExitCode: &two, Stdout: "--- FAIL: TestAdd (0.00s)\n    main_test.go:15: Add(2, 2) = 5, want 4\n", DurationMS: 40},
	}}
	harness := newChecksHarness(t, runner)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_check_run2"})
	runID := extractField(t, rec, "id")

	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/checks",
		`{"workspace_id":"wsp_1","profiles":["go.test"]}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_check_2"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"verdict":"red"`) {
		t.Fatalf("verdict missing: %s", body)
	}
	if !strings.Contains(body, "TestAdd") || !strings.Contains(body, "main_test.go") {
		t.Fatalf("parsed failures missing: %s", body)
	}
}

func TestRunChecksRejectsUnknownProfile(t *testing.T) {
	harness := newChecksHarness(t, &fakeCheckRunner{})
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_check_run3"})
	runID := extractField(t, rec, "id")
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/checks",
		`{"workspace_id":"wsp_1","profiles":["rm -rf /"]}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_check_3"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unknown command profile") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRunChecksRejectsUnknownWorkspace(t *testing.T) {
	harness := newChecksHarness(t, &fakeCheckRunner{})
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_check_run4"})
	runID := extractField(t, rec, "id")
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/checks",
		`{"workspace_id":"nope","profiles":["go.test"]}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_check_4"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestCheckLogsEndpoints(t *testing.T) {
	two := 2
	runner := &fakeCheckRunner{results: map[string]checks.Result{
		"go.test": {Status: "failed", ExitCode: &two, Stdout: "--- FAIL: TestAdd", DurationMS: 10},
	}}
	harness := newChecksHarness(t, runner)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_logs_run"})
	runID := extractField(t, rec, "id")
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/checks",
		`{"workspace_id":"wsp_1","profiles":["go.test"]}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_logs_1"})
	checkID := extractNestedField(t, rec, "items.0.id")

	rec = harness.doRequest(t, http.MethodGet, "/api/v1/runs/"+runID+"/checks/"+checkID+"/logs", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("logs status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"verdict":"red"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}

	rec = harness.doRequest(t, http.MethodGet, "/api/v1/runs/"+runID+"/checks/chk_missing/logs", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown check status = %d, want 404", rec.Code)
	}
}

func TestChecksUnavailableWithoutService(t *testing.T) {
	harness := newChecksHarness(t, &fakeCheckRunner{})
	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          harness.harnessServices(),
		Auth:              apiAuthServices(harness, &fakeOrgs{}),
		Encryptor:         harness.encryptor,
		Runs:              newFakeRunsStore(),
		Queue:             &fakeTaskQueue{},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	harness.handler = server.Handler()
	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/run_x/checks", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// extractNestedField reads a dotted path from a JSON object response.
func extractNestedField(t *testing.T, rec *httptest.ResponseRecorder, path string) string {
	t.Helper()
	var payload map[string]any
	if err := jsonUnmarshalBytes(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	parts := strings.Split(path, ".")
	var current any = payload
	for _, part := range parts {
		if index, err := strconv.Atoi(part); err == nil {
			asSlice, ok := current.([]any)
			if !ok || index >= len(asSlice) {
				t.Fatalf("path %q not found", path)
			}
			current = asSlice[index]
			continue
		}
		asMap, ok := current.(map[string]any)
		if !ok {
			t.Fatalf("path %q not found", path)
		}
		current = asMap[part]
	}
	value, ok := current.(string)
	if !ok || value == "" {
		t.Fatalf("path %q is not a string", path)
	}
	return value
}

func jsonUnmarshalBytes(data []byte, out any) error {
	return json.Unmarshal(data, out)
}

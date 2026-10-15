package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// fakeConsoleStores mirrors the durable console stores, including the way a
// failure is surfaced rather than hidden.
type fakeConsoleStores struct {
	dashboard store.DashboardRow
	dashErr   error
	prefs     store.UserPreferences
	found     bool
	prefsErr  error
	savedView map[string]any
	saveCalls int
}

func (f *fakeConsoleStores) Dashboard(_ context.Context, _ string) (store.DashboardRow, error) {
	if f.dashErr != nil {
		return store.DashboardRow{}, f.dashErr
	}
	return f.dashboard, nil
}

func (f *fakeConsoleStores) GetPreferences(_ context.Context, _ string) (store.UserPreferences, bool, error) {
	if f.prefsErr != nil {
		return store.UserPreferences{}, false, f.prefsErr
	}
	return f.prefs, f.found, nil
}

func (f *fakeConsoleStores) SavePreferences(_ context.Context, userID, _ string, view map[string]any) (store.UserPreferences, error) {
	f.saveCalls++
	f.savedView = view
	if f.prefsErr != nil {
		return store.UserPreferences{}, f.prefsErr
	}
	f.prefs = store.UserPreferences{UserID: userID, View: view, UpdatedAt: time.Now().UTC()}
	return f.prefs, nil
}

func newConsoleHarness(t *testing.T, stores ConsoleStores) m02Harness {
	t.Helper()
	base := newM02Harness(t, config.EnvTest)
	base.identities.byEmail["owner@example.test"] = "hashed:" + testPassword
	base.runs = newFakeRunsStore()
	base.queue = &fakeTaskQueue{}
	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          base.harnessServices(),
		Auth:              apiAuthServices(base, &fakeOrgs{}),
		Encryptor:         base.encryptor,
		Runs:              base.runs,
		Queue:             base.queue,
		Console:           stores,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	return base
}

func dashboardFixture() store.DashboardRow {
	return store.DashboardRow{
		RunsByState:       map[string]int{"completed": 3, "blocked": 1, "failed": 2},
		OpenIssues:        7,
		BlockedRuns:       1,
		FailedRuns:        2,
		RecentAuditEvents: 12,
		RecentRuns: []store.DashboardRun{
			{ID: "run_recent", ProjectID: "prj_1", ProjectName: "Fixture project", State: "blocked", Attempt: 2},
		},
		GeneratedAt: time.Now().UTC(),
	}
}

func TestDashboardReportsRealAggregates(t *testing.T) {
	stores := &fakeConsoleStores{dashboard: dashboardFixture(), found: false}
	harness := newConsoleHarness(t, stores)
	token, _ := harness.login(t, "owner@example.test", testPassword)

	rec := harness.doRequest(t, http.MethodGet, "/api/v1/dashboard", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"open_issues":7`,
		`"blocked_runs":1`,
		`"failed_runs":2`,
		`"recent_audit_events":12`,
		`"id":"run_recent"`,
		`"project_name":"Fixture project"`,
		`"state":"blocked"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestDashboardSurfacesAStoreFailure(t *testing.T) {
	stores := &fakeConsoleStores{dashErr: errors.New("postgres is down")}
	harness := newConsoleHarness(t, stores)
	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/dashboard", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the aggregate cannot be read", rec.Code)
	}
}

func TestPreferencesRoundTrip(t *testing.T) {
	stores := &fakeConsoleStores{found: false}
	harness := newConsoleHarness(t, stores)
	token, csrf := harness.login(t, "owner@example.test", testPassword)

	// A new caller gets the empty document, which the UI renders as defaults.
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/me/preferences", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"stored":false`) {
		t.Fatalf("body = %s", rec.Body.String())
	}

	rec = harness.doRequest(t, http.MethodPut, "/api/v1/me/preferences",
		`{"view":{"table_density":"comfortable","active_section":"runs","filters":{"state":"blocked"}}}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_prefs_1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("save status = %d body = %s", rec.Code, rec.Body.String())
	}
	if stores.savedView["table_density"] != "comfortable" {
		t.Fatalf("stored view = %v", stores.savedView)
	}

	// The stored document is returned unchanged.
	stores.found = true
	rec = harness.doRequest(t, http.MethodGet, "/api/v1/me/preferences", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"comfortable"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestPreferencesRejectsMalformedPayloads(t *testing.T) {
	stores := &fakeConsoleStores{}
	harness := newConsoleHarness(t, stores)
	token, csrf := harness.login(t, "owner@example.test", testPassword)

	rec := harness.doRequest(t, http.MethodPut, "/api/v1/me/preferences", `{"view":`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_prefs_bad"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for malformed JSON", rec.Code)
	}
	if stores.saveCalls != 0 {
		t.Fatal("a malformed document must not be stored")
	}

	// A document that cannot round-trip is refused as validation, not a 500.
	rec = harness.doRequest(t, http.MethodPut, "/api/v1/me/preferences", `{"view":`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_prefs_bad2"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestConsoleUnavailableWithoutService(t *testing.T) {
	harness := newConsoleHarness(t, nil)
	token, _ := harness.login(t, "owner@example.test", testPassword)
	for _, path := range []string{"/api/v1/dashboard", "/api/v1/me/preferences"} {
		rec := harness.doRequest(t, http.MethodGet, path, "",
			map[string]string{"Cookie": sessionCookieName + "=" + token})
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", path, rec.Code)
		}
	}
}

func TestDashboardRequiresAuthentication(t *testing.T) {
	harness := newConsoleHarness(t, &fakeConsoleStores{dashboard: dashboardFixture()})
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/dashboard", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

// runStateFilterEnv is the harness for the runs list state filter test.
func TestListRunsStateFilterIsRejectedWhenUnknown(t *testing.T) {
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
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	token, _ := base.login(t, "owner@example.test", testPassword)

	rec := base.doRequest(t, http.MethodGet, "/api/v1/runs?project_id=prj_1&state=not_a_state", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown state", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "state") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

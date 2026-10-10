package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
)

type pingDB struct{ err error }

func (p pingDB) Ping(ctx context.Context) error { return p.err }

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{
		Process:  config.ProcessAPI,
		Env:      config.EnvTest,
		HTTPAddr: ":8080",
	}
	return cfg
}

func newTestServer(t *testing.T, dbErr error, pending []migrations.Migration, pendingErr error) (*Server, http.Handler) {
	t.Helper()
	srv, err := NewServer(Options{
		Config: testConfig(t),
		DB:     pingDB{err: dbErr},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) {
			return pending, pendingErr
		},
		Version:   "test-version",
		GitCommit: "test-commit",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv, srv.Handler()
}

func TestHealthLiveAlwaysOK(t *testing.T) {
	_, handler := newTestServer(t, errors.New("db down"), nil, nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/live", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	var body liveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("status = %q", body.Status)
	}
}

func TestHealthReadyWhenHealthy(t *testing.T) {
	_, handler := newTestServer(t, nil, nil, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/ready", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ready" || len(body.Checks) != 2 {
		t.Fatalf("body = %+v", body)
	}
}

func TestHealthReadyWhenDatabaseUnreachable(t *testing.T) {
	_, handler := newTestServer(t, errors.New("connection refused"), nil, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "unavailable" {
		t.Fatalf("status = %q", body.Status)
	}
	if body.Checks[0].Name != "database" || body.Checks[0].Status != "down" {
		t.Fatalf("checks = %+v", body.Checks)
	}
}

func TestHealthReadyWhenCriticalMigrationPending(t *testing.T) {
	_, handler := newTestServer(t, nil, []migrations.Migration{{Version: 2, Name: "add_runs", Critical: true}}, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/ready", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body readinessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	found := false
	for _, c := range body.Checks {
		if c.Name == "migrations" && c.Status == "down" && strings.Contains(c.Detail, "0002_add_runs.up.sql") {
			found = true
		}
	}
	if !found {
		t.Fatalf("migrations check missing detail: %+v", body.Checks)
	}
}

// fakeDirectory fails every resolution: if the subject middleware wrapped the
// health routes, provisioning would fail and readiness would become 401.
type fakeDirectory struct{}

func (fakeDirectory) ProvisionUser(context.Context, string) (string, error) {
	return "", errors.New("directory unavailable")
}

func (fakeDirectory) FirstOrganization(context.Context, string) (string, error) {
	return "", errors.New("directory unavailable")
}

func (fakeDirectory) Membership(context.Context, string, string) (string, error) {
	return "", errors.New("directory unavailable")
}

func TestHealthRoutesBypassSubjectMiddleware(t *testing.T) {
	// Production configuration deliberately disables the development subject
	// resolver, yet liveness and readiness must keep answering so the outage
	// is reported as 503 rather than 401.
	cfg := &config.Config{Process: config.ProcessAPI, Env: config.EnvProduction, HTTPAddr: ":8080"}
	srv, err := NewServer(Options{
		Config: cfg,
		DB:     pingDB{err: errors.New("database unreachable")},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) {
			return nil, nil
		},
		SubjectDirectory: fakeDirectory{},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := srv.Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/live", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("liveness status = %d, want 200 (health routes must not require a subject)", rec.Code)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("readiness status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unavailable") {
		t.Fatalf("readiness body = %s", rec.Body.String())
	}

	// The authenticated surface still rejects the caller in production.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/organizations", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("api status = %d, want 401 in production", rec.Code)
	}
}

func TestSystemInfo(t *testing.T) {
	_, handler := newTestServer(t, nil, nil, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/system/info", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body systemInfoResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Name != "reposurgeon" || body.Version != "test-version" || body.GitCommit != "test-commit" {
		t.Fatalf("body = %+v", body)
	}
	if !strings.HasPrefix(body.GoVersion, "go") {
		t.Fatalf("go_version = %q", body.GoVersion)
	}
	if body.StartedAt.IsZero() {
		t.Fatal("started_at must be set")
	}
}

func TestUnknownRouteReturnsJSON404(t *testing.T) {
	_, handler := newTestServer(t, nil, nil, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("content-type = %q", rec.Header().Get("Content-Type"))
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Error.Code != "not_found" || body.Error.RequestID == "" {
		t.Fatalf("body = %+v", body)
	}
}

func TestWrongMethodReturnsJSON405(t *testing.T) {
	_, handler := newTestServer(t, nil, nil, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/health/live", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405; body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "method_not_allowed") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRequestIDPropagatesAndGenerates(t *testing.T) {
	_, handler := newTestServer(t, nil, nil, nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/live", nil))
	if got := rec.Header().Get("X-Request-ID"); !strings.HasPrefix(got, "req_") {
		t.Fatalf("generated request id = %q", got)
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/live", nil)
	req.Header.Set("X-Request-ID", "client-supplied-id")
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-ID"); got != "client-supplied-id" {
		t.Fatalf("echoed request id = %q", got)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/system/info", nil)
	req.Header.Set("X-Request-ID", "")
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Request-ID"); !strings.HasPrefix(got, "req_") {
		t.Fatalf("empty request id should be replaced, got %q", got)
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	_, handler := newTestServer(t, nil, nil, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/live", nil))
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers = %v", rec.Header())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache-control = %q", rec.Header().Get("Cache-Control"))
	}
}

func TestCORSNotEmittedWithoutAllowlist(t *testing.T) {
	_, handler := newTestServer(t, nil, nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/live", nil)
	req.Header.Set("Origin", "https://evil.example.com")
	handler.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("unexpected CORS header %q", got)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/health/live", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("bare OPTIONS status = %d", rec.Code)
	}
}

func TestCORSEmittedForAllowlistedOrigin(t *testing.T) {
	srv, err := NewServer(Options{
		Config: &config.Config{
			Process:            config.ProcessAPI,
			Env:                config.EnvTest,
			CORSAllowedOrigins: []string{"http://localhost:5173"},
		},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/live", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	srv.Handler().ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("ACAO = %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestNewServerRequiresOptions(t *testing.T) {
	if _, err := NewServer(Options{}); err == nil {
		t.Fatal("expected error for empty options")
	}
	if _, err := NewServer(Options{Config: testConfig(t)}); err == nil {
		t.Fatal("expected error for missing db")
	}
}

func TestPanicBecomesJSON500(t *testing.T) {
	panicHandler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})
	wrapped := recoverMiddleware(nil, panicHandler)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/health/live", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal_error") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

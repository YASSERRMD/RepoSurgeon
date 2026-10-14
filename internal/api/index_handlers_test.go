package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/contextbuild"
	"github.com/YASSERRMD/RepoSurgeon/internal/index"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
)

// fakeContextStore is an in-memory manifest store for handler tests.
type fakeContextStore struct {
	saved   []contextbuild.Manifest
	listErr error
}

func (f *fakeContextStore) SaveManifest(_ context.Context, m contextbuild.Manifest) error {
	f.saved = append(f.saved, m)
	return nil
}

func (f *fakeContextStore) LoadManifest(_ context.Context, id string) (contextbuild.Manifest, error) {
	for _, m := range f.saved {
		if m.ID == id {
			return m, nil
		}
	}
	return contextbuild.Manifest{}, errManifestNotFound
}

func (f *fakeContextStore) ListManifests(_ context.Context, _ string, _ int) ([]contextbuild.Manifest, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.saved, nil
}

// newTestIndex builds an index backed by a real database and seeded with a
// fixture workspace, so context building exercises the real retrieval path.
func newTestIndex(t *testing.T) *index.Index {
	t.Helper()
	pool := openIndexPool(t)
	ctx := context.Background()
	orgID := ids.MustNew("org")
	if _, err := pool.Exec(ctx, `
		INSERT INTO organizations (id, slug, name) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING`, orgID, orgID, "context test org"); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	dir := fixtureIndexWorkspace(t)
	ix := index.New(pool, dir)
	// The workspace id is fixed per run so the cleanup below removes only this
	// test's derived rows. Wiping the table would destroy the rows of the
	// index package's tests, which run against the same database in parallel.
	ix.WorkspaceID = "wsp_ctx_" + ids.MustNew("wsp")
	for _, table := range []string{"repo_files", "repo_symbols"} {
		if _, err := pool.Exec(ctx, "DELETE FROM "+table+" WHERE workspace_id = $1", ix.WorkspaceID); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	if _, err := ix.Rebuild(ctx, ix.WorkspaceID, orgID); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	return ix
}

// fixtureIndexWorkspace writes the fixture files used by index tests.
func fixtureIndexWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":  "module example.com/ctx\n\ngo 1.25\n",
		"main.go": "package main\n\nfunc Add(a, b int) int { return a + b }\n",
	}
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

func newIndexHarness(t *testing.T) m02Harness {
	t.Helper()
	base := newM02Harness(t, config.EnvTest)
	base.identities.byEmail["owner@example.test"] = "hashed:" + testPassword

	repoIndex := newTestIndex(t)
	store := &fakeContextStore{}
	contextSvc := NewContextService(repoIndex, contextbuild.NewBuilder(repoIndex), store)

	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          base.harnessServices(),
		Auth:              apiAuthServices(base, &fakeOrgs{}),
		Encryptor:         base.encryptor,
		Runs:              newFakeRunsStore(),
		Queue:             &fakeTaskQueue{},
		Context:           contextSvc,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	return base
}

func TestWorkspaceSearchRequiresQuery(t *testing.T) {
	harness := newIndexHarness(t)
	token, _ := harness.login(t, "owner@example.test", testPassword)
	for _, url := range []string{"/api/v1/workspaces/wsp_1/search", "/api/v1/workspaces/wsp_1/search?q="} {
		rec := harness.doRequest(t, http.MethodGet, url, "",
			map[string]string{"Cookie": sessionCookieName + "=" + token})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s status = %d, want 400", url, rec.Code)
		}
	}
}

func TestWorkspaceSearchRejectsUnknownWorkspace(t *testing.T) {
	harness := newIndexHarness(t)
	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/workspaces/nope/search?q=Add", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestRunContextRefreshBuildsAndStoresManifest(t *testing.T) {
	harness := newIndexHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)

	// Create a run first so the context endpoint can find it.
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_ctx_run"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("run create status = %d body = %s", rec.Code, rec.Body.String())
	}
	runID := extractField(t, rec, "id")

	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/context/refresh",
		`{"workspace_id":"wsp_ctx_1","query":"Add","token_budget":32768}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_ctx_1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("refresh status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// The manifest must report provenance and the untrusted notice.
	for _, want := range []string{`"token_estimate"`, `"sha256"`, `"reason"`, `"untrusted_notes"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("manifest missing %s: %s", want, body)
		}
	}
	var manifest contextbuild.Manifest
	if err := json.Unmarshal(rec.Body.Bytes(), &manifest); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if manifest.TokenEstimate > manifest.TokenBudget {
		t.Fatalf("estimate %d exceeds budget %d", manifest.TokenEstimate, manifest.TokenBudget)
	}

	// The run context endpoint returns the stored manifest.
	rec = harness.doRequest(t, http.MethodGet, "/api/v1/runs/"+runID+"/context", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("context get status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestRunContextRefreshRejectsUnknownWorkspace(t *testing.T) {
	harness := newIndexHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/run_x/context/refresh",
		`{"workspace_id":"nope","query":"Add"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_ctx_2"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

// openIndexPool connects to the test database, skipping when unset.
func openIndexPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("RS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RS_TEST_DATABASE_URL not set; skipping index handler test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

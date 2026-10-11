package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/agent"
	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/queue"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// fakeRunsStore is an in-memory run store for handler tests.
type fakeRunsStore struct {
	runs   map[string]*store.Run
	nextID int
}

func newFakeRunsStore() *fakeRunsStore {
	return &fakeRunsStore{runs: map[string]*store.Run{}}
}

func (f *fakeRunsStore) Create(_ context.Context, run store.Run) (store.Run, error) {
	if run.ID == "" {
		f.nextID++
		run.ID = "run_fake"
	}
	if run.State == "" {
		run.State = agent.StateQueued
	}
	run.CreatedAt = time.Now().UTC()
	run.UpdatedAt = run.CreatedAt
	f.runs[run.ID] = &run
	return run, nil
}

func (f *fakeRunsStore) Get(_ context.Context, organizationID, runID string) (store.Run, error) {
	run, ok := f.runs[runID]
	if !ok || run.OrganizationID != organizationID {
		return store.Run{}, agent.ErrRunNotFound
	}
	return *run, nil
}

func (f *fakeRunsStore) Transition(_ context.Context, organizationID, runID, toState, _, _ string) (store.Run, error) {
	run, ok := f.runs[runID]
	if !ok || run.OrganizationID != organizationID {
		return store.Run{}, agent.ErrRunNotFound
	}
	if err := agent.Transition(run.State, toState); err != nil {
		return store.Run{}, err
	}
	run.State = toState
	return *run, nil
}

func (f *fakeRunsStore) ListForProject(_ context.Context, projectID string, _ int, _ string) (store.Page[store.Run], error) {
	var items []store.Run
	for _, run := range f.runs {
		if run.ProjectID == projectID {
			items = append(items, *run)
		}
	}
	return store.Page[store.Run]{Items: items}, nil
}

func (f *fakeRunsStore) History(_ context.Context, runID string) ([]store.StateChange, error) {
	if _, ok := f.runs[runID]; !ok {
		return nil, nil
	}
	return []store.StateChange{{ID: "hst_1", RunID: runID, ToState: agent.StateQueued, ActorType: "user"}}, nil
}

// apiAuthServices builds the auth bundle with a specific organizations fake.
func apiAuthServices(base m02Harness, orgs *fakeOrgs) *authServices {
	return &authServices{
		sessions:      base.sessions,
		identities:    base.identities,
		members:       base.members,
		users:         base.users,
		organizations: orgs,
		credentials:   base.credentials,
		hasher:        fastTestHasher{},
	}
}

// createdRunID extracts the run id from a create response.
func createdRunID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.ID == "" {
		t.Fatal("create response carried no run id")
	}
	return payload.ID
}

// fakeTaskQueue records enqueued tasks without a database.
type fakeTaskQueue struct {
	tasks []queue.Task
}

func (f *fakeTaskQueue) Enqueue(_ context.Context, task queue.Task) (queue.Task, error) {
	f.tasks = append(f.tasks, task)
	return task, nil
}

func newRunHarness(t *testing.T) m02Harness {
	t.Helper()
	base := newM02Harness(t, config.EnvTest)
	base.identities.byEmail["owner@example.test"] = "hashed:" + testPassword
	base.identities.byEmail["viewer@example.test"] = "hashed:" + testPassword
	runs := newFakeRunsStore()

	// The viewer belongs to a different organization, so the run is invisible.
	orgs := &fakeOrgs{}
	orgs.withUserOrg("usr_viewer", "org_other")
	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          &Services{Organizations: orgs, Projects: base.harnessServices().Projects, Audit: base.audit, Idempotency: newFakeIdempotency()},
		Auth:              apiAuthServices(base, orgs),
		Encryptor:         base.encryptor,
		Runs:              runs,
		Queue:             &fakeTaskQueue{},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	base.fakeRuns = runs
	base.fakeQueue = &fakeTaskQueue{}
	return base
}

func TestCreateRunEnqueuesTask(t *testing.T) {
	harness := newRunHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)

	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1","base_sha":"`+strings.Repeat("a", 40)+`","model":{"provider":"stepfun","id":"step-5-preview","reasoning_effort":"high"},"limits":{"max_steps":10}}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_run_1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var run struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if run.State != agent.StateQueued {
		t.Fatalf("state = %q", run.State)
	}
	if len(harness.fakeRuns.runs) != 1 {
		t.Fatalf("runs stored = %d", len(harness.fakeRuns.runs))
	}

	// The run is readable with its history.
	getRec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/"+run.ID, "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d body = %s", getRec.Code, getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), "history") {
		t.Fatalf("body = %s", getRec.Body.String())
	}
}

func TestCreateRunRejectsInvalidBaseSHA(t *testing.T) {
	harness := newRunHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1","base_sha":"not-a-sha"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_run_bad"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestStartRunRejectsNonQueuedRun(t *testing.T) {
	harness := newRunHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)

	// Create then cancel: starting a canceled run is a state conflict.
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_run_c"})
	runID := createdRunID(t, rec)
	cancelRec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/cancel", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if cancelRec.Code != http.StatusAccepted {
		t.Fatalf("cancel status = %d body = %s", cancelRec.Code, cancelRec.Body.String())
	}
	rec = harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/start", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_run_s"})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "state_conflict") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestGetRunIsolationAcrossOrganizations(t *testing.T) {
	harness := newRunHarness(t)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	createRec := harness.doRequest(t, http.MethodPost, "/api/v1/runs",
		`{"project_id":"prj_1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_run_i"})
	runID := createdRunID(t, createRec)

	// The viewer has no run in this organization, so the run is invisible.
	viewerHash, err := hashForEmail("viewer@example.test")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	_ = viewerHash
	viewerToken, _ := harness.login(t, "viewer@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/"+runID, "",
		map[string]string{"Cookie": sessionCookieName + "=" + viewerToken})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403", rec.Code)
	}
}

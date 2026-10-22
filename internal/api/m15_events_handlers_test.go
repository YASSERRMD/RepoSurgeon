package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// fakeEventsStore mirrors the durable event log for handler tests.
type fakeEventsStore struct {
	events  []domain.RunEvent
	listErr error
}

func (f *fakeEventsStore) Create(_ context.Context, event domain.RunEvent) (domain.RunEvent, error) {
	event.Sequence = int64(len(f.events) + 1)
	event.CreatedAt = time.Now().UTC()
	f.events = append(f.events, event)
	return event, nil
}

func (f *fakeEventsStore) List(_ context.Context, organizationID, runID string, limit int, cursor string) (domain.Page[domain.RunEvent], error) {
	if f.listErr != nil {
		return domain.Page[domain.RunEvent]{}, f.listErr
	}
	items := make([]domain.RunEvent, 0, len(f.events))
	for _, ev := range f.events {
		if ev.OrganizationID == organizationID && ev.RunID == runID {
			items = append(items, ev)
		}
	}
	return domain.Page[domain.RunEvent]{Items: items}, nil
}

func (f *fakeEventsStore) ListRecent(_ context.Context, organizationID string, _ string, limit int, cursor string) (domain.Page[domain.RunEvent], error) {
	if f.listErr != nil {
		return domain.Page[domain.RunEvent]{}, f.listErr
	}
	items := make([]domain.RunEvent, 0, len(f.events))
	for _, ev := range f.events {
		if ev.OrganizationID == organizationID {
			items = append(items, ev)
		}
	}
	return domain.Page[domain.RunEvent]{Items: items}, nil
}

func newEventsHarness(t *testing.T, events EventsStore) m02Harness {
	t.Helper()
	base := newM02Harness(t, config.EnvTest)
	base.identities.byEmail["owner@example.test"] = "hashed:" + testPassword
	runs := newFakeRunsStore()
	_, _ = runs.Create(context.Background(), store.Run{
		ID:             "run_owner",
		OrganizationID: "org_1",
		ProjectID:      "prj_1",
		CreatedBy:      "usr_owner",
	})
	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          base.harnessServices(),
		Auth:              apiAuthServices(base, &fakeOrgs{}),
		Encryptor:         base.encryptor,
		Runs:              runs,
		Queue:             &fakeTaskQueue{},
		Events:            events,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	return base
}

func TestListRunEventsReturnsTheRunPage(t *testing.T) {
	events := &fakeEventsStore{}
	harness := newEventsHarness(t, events)

	ctx := context.Background()
	_, _ = events.Create(ctx, domain.RunEvent{
		OrganizationID: "org_1",
		RunID:          "run_owner",
		CausationID:    "cause_1",
		Type:           "step.began",
		Step:           "investigate",
		Payload:        map[string]any{"attempt": 1},
	})
	_, _ = events.Create(ctx, domain.RunEvent{
		OrganizationID: "org_1",
		RunID:          "run_owner",
		CausationID:    "cause_2",
		Type:           "tool.called",
		Payload:        map[string]any{"tool": "read"},
	})

	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/run_owner/events", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"type":"step.began"`,
		`"type":"tool.called"`,
		`"run_id":"run_owner"`,
		`"organization_id":"org_1"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestListRunEventsIsTenantScoped(t *testing.T) {
	events := &fakeEventsStore{}
	harness := newEventsHarness(t, events)

	ctx := context.Background()
	_, _ = events.Create(ctx, domain.RunEvent{
		OrganizationID: "org_1",
		RunID:          "run_owner",
		CausationID:    "cause_1",
		Type:           "step.began",
	})

	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/run_owner/events?after=", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []domain.RunEvent `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected 1 event, got %d", len(page.Items))
	}
	if page.Items[0].OrganizationID != "org_1" {
		t.Fatalf("cross-tenant leak: %s", page.Items[0].OrganizationID)
	}
}

func TestListRunEventsSurfacesStoreFailure(t *testing.T) {
	events := &fakeEventsStore{listErr: context.DeadlineExceeded}
	harness := newEventsHarness(t, events)

	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/run_owner/events", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code == http.StatusOK {
		t.Fatalf("expected non-200, got %d", rec.Code)
	}
}

func TestStreamRunEventsSetsSSEHeaders(t *testing.T) {
	events := &fakeEventsStore{}
	harness := newEventsHarness(t, events)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/runs/run_owner/events/stream", nil)
	token, _ := harness.login(t, "owner@example.test", testPassword)
	req.Header.Set("Cookie", sessionCookieName+"="+token)

	rec := httptest.NewRecorder()
	harness.handler.ServeHTTP(rec, req)

	// The handler blocks until the context expires; the status and headers are
	// recorded once it returns.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", got)
	}
	if got := rec.Header().Get("Cache-Control"); !strings.Contains(got, "no-cache") {
		t.Fatalf("cache-control = %q, want no-cache", got)
	}
}

func TestStreamRunEventsRejectsUnknownRun(t *testing.T) {
	events := &fakeEventsStore{}
	harness := newEventsHarness(t, events)

	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/does_not_exist/events/stream", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if errorCode(rec) != "not_found" {
		t.Fatalf("code = %q, want not_found", errorCode(rec))
	}
}

func TestListOrgEventsReturnsOrgPage(t *testing.T) {
	events := &fakeEventsStore{}
	harness := newEventsHarness(t, events)

	ctx := context.Background()
	_, _ = events.Create(ctx, domain.RunEvent{
		OrganizationID: "org_1",
		RunID:          "run_owner",
		CausationID:    "cause_1",
		Type:           "step.began",
	})
	_, _ = events.Create(ctx, domain.RunEvent{
		OrganizationID: "org_other",
		RunID:          "run_other",
		CausationID:    "cause_other",
		Type:           "tool.called",
	})

	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/events", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"type":"step.began"`) {
		t.Fatalf("body missing org_1 event: %s", body)
	}
	if strings.Contains(body, `"type":"tool.called"`) {
		t.Fatalf("body should not contain cross-tenant event: %s", body)
	}
}

package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// fakeCostStore mirrors the durable cost aggregate for handler tests.
type fakeCostStore struct {
	aggregate store.CostAggregate
	err       error
}

func (f *fakeCostStore) Aggregate(_ context.Context, _ string) (store.CostAggregate, error) {
	return f.aggregate, f.err
}

func newCostHarness(t *testing.T, cost CostStore) m02Harness {
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
		Cost:              cost,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	return base
}

func TestGetCostReportsMeasuredUsage(t *testing.T) {
	cost := &fakeCostStore{aggregate: store.CostAggregate{
		OrganizationID: "org_1",
		TotalUSD:       1.234567,
		TotalTokens:    12500,
		ByModel: []store.UsageRow{
			{Model: "step-5-preview", InputTokensCached: 3000, InputTokensUncached: 4500, OutputTokens: 2000, UsdCost: 0.891234},
			{Model: "step-3.7-flash", InputTokensCached: 1000, InputTokensUncached: 1500, OutputTokens: 500, UsdCost: 0.343333},
		},
		GeneratedAt: time.Now().UTC(),
	}}
	harness := newCostHarness(t, cost)

	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/metrics/cost", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"total_usd":1.234567`,
		`"total_tokens":12500`,
		`"model":"step-5-preview"`,
		`"input_tokens_cached":3000`,
		`"input_tokens_uncached":4500`,
		`"output_tokens":2000`,
		`"usd_cost":0.891234`,
		`"model":"step-3.7-flash"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestGetCostSurfacesStoreFailure(t *testing.T) {
	cost := &fakeCostStore{err: context.DeadlineExceeded}
	harness := newCostHarness(t, cost)

	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/metrics/cost", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code == http.StatusOK {
		t.Fatalf("expected non-200, got %d", rec.Code)
	}
}

func TestGetAlertsFiresRunawayCost(t *testing.T) {
	cost := &fakeCostStore{aggregate: store.CostAggregate{
		OrganizationID: "org_1",
		TotalUSD:       150.0,
		TotalTokens:    50000,
		GeneratedAt:    time.Now().UTC(),
	}}
	harness := newCostHarness(t, cost)

	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/alerts", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"severity":"critical"`,
		`"condition":"runaway_cost"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestGetAlertsEmptyWhenUnderLimit(t *testing.T) {
	cost := &fakeCostStore{aggregate: store.CostAggregate{
		OrganizationID: "org_1",
		TotalUSD:       5.0,
		GeneratedAt:    time.Now().UTC(),
	}}
	harness := newCostHarness(t, cost)

	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/alerts", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("expected empty alerts, got %s", rec.Body.String())
	}
}

// fakeExportStore mirrors the durable export metadata for handler tests.
type fakeExportStore struct {
	record store.ExportRecord
	found  bool
	err    error
}

func (f *fakeExportStore) Create(_ context.Context, _, _, _ string, _ time.Time) (store.ExportRecord, error) {
	return f.record, f.err
}
func (f *fakeExportStore) GetByRun(_ context.Context, _, _ string) (store.ExportRecord, bool, error) {
	return f.record, f.found, f.err
}
func (f *fakeExportStore) UpdateStatus(_ context.Context, _, _ string) error {
	return f.err
}

func newExportHarness(t *testing.T, cost CostStore, exports ExportStore, events EventsStore) m02Harness {
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
		Cost:              cost,
		Events:            events,
		Exports:           exports,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	return base
}

func TestGetExportReturnsSanitizedTrace(t *testing.T) {
	exports := &fakeExportStore{record: store.ExportRecord{
		ID:             "exp_1",
		OrganizationID: "org_1",
		RunID:          "run_owner",
		Format:         "json",
		Status:         "ready",
	}}
	events := &fakeEventsStore{}
	ctx := context.Background()
	_, _ = events.Create(ctx, domain.RunEvent{
		OrganizationID: "org_1",
		RunID:          "run_owner",
		CausationID:    "cause_1",
		Type:           "step.began",
	})
	harness := newExportHarness(t, &fakeCostStore{}, exports, events)

	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/run_owner/export?format=json", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"run_id":"run_owner"`,
		`"type":"step.began"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestGetExportRejectsUnknownRun(t *testing.T) {
	exports := &fakeExportStore{}
	harness := newExportHarness(t, &fakeCostStore{}, exports, &fakeEventsStore{})

	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/does_not_exist/export", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

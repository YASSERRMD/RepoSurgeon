package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
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

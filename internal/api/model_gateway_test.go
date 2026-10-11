package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/model"
)

// newGatewayHarness builds a server with the fake model provider injected so
// no live StepFun account is required.
func newGatewayHarness(t *testing.T, provider model.Provider, key string) (m02Harness, *fakeSessions) {
	t.Helper()
	base := newM02Harness(t, config.EnvTest)
	// The gateway harness uses the fast hasher, so align the seeded identity.
	base.identities.byEmail["owner@example.test"] = "hashed:" + testPassword
	base.identities.byEmail["viewer@example.test"] = "hashed:" + testPassword
	gateway := &ModelGateway{
		config: config.ModelConfig{
			StepFunAPIKey:   key,
			StepFunBaseURL:  "https://api.stepfun.ai/v1",
			DefaultModelID:  model.DefaultStepFunModel,
			ReasoningEffort: "medium",
		},
		provider: provider,
	}
	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          base.harnessServices(),
		Auth:              base.harnessAuth(),
		Encryptor:         base.encryptor,
		Gateway:           gateway,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	return base, base.sessions
}

func (h m02Harness) harnessServices() *Services {
	return &Services{
		Organizations: fakeOrgs{},
		Projects:      &fakeProjects{},
		Audit:         h.audit,
		Idempotency:   newFakeIdempotency(),
	}
}

func (h m02Harness) harnessAuth() *authServices {
	return &authServices{
		sessions:      h.sessions,
		identities:    h.identities,
		members:       h.members,
		users:         h.users,
		organizations: fakeOrgs{},
		credentials:   h.credentials,
		hasher:        fastTestHasher{},
	}
}

// fastTestHasher keeps login cheap in tests.
type fastTestHasher struct{}

func (fastTestHasher) Hash(password string) (string, error) { return "hashed:" + password, nil }
func (fastTestHasher) Verify(encoded, password string) (bool, error) {
	return encoded == "hashed:"+password, nil
}

func TestModelStatusReportsConfigurationWithoutSecrets(t *testing.T) {
	harness, _ := newGatewayHarness(t, model.NewFakeProvider(), "sf-live-secret")
	token, _ := harness.login(t, "owner@example.test", testPassword)

	rec := harness.doRequest(t, http.MethodGet, "/api/v1/model/provider", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, "sf-live-secret") {
		t.Fatal("api key leaked in the status response")
	}
	if !strings.Contains(body, "step-5-preview") {
		t.Fatalf("body = %s", body)
	}
	if !strings.Contains(body, `"key_configured":true`) {
		t.Fatalf("body = %s", body)
	}
}

func TestModelStatusRequiresPermission(t *testing.T) {
	harness, _ := newGatewayHarness(t, model.NewFakeProvider(), "sf-live-secret")
	token, _ := harness.login(t, "viewer@example.test", testPassword)

	rec := harness.doRequest(t, http.MethodGet, "/api/v1/model/provider", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestModelProbeUsesConfiguredProvider(t *testing.T) {
	provider := model.NewFakeProvider(model.TextResponse("ready"))
	harness, _ := newGatewayHarness(t, provider, "sf-live-secret")
	token, csrf := harness.login(t, "owner@example.test", testPassword)

	rec := harness.doRequest(t, http.MethodPost, "/api/v1/model/probe", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "prompt_tokens") || !strings.Contains(body, "latency_ms") {
		t.Fatalf("body = %s", body)
	}
	if len(provider.Requests()) != 1 {
		t.Fatalf("provider requests = %d, want 1", len(provider.Requests()))
	}
	request := provider.Requests()[0]
	if request.Model != model.DefaultStepFunModel || request.ReasoningEffort != model.ReasoningMedium {
		t.Fatalf("request = %+v", request)
	}
}

func TestModelProbeWithoutProviderReportsUnavailable(t *testing.T) {
	harness, _ := newGatewayHarness(t, nil, "")
	token, csrf := harness.login(t, "owner@example.test", testPassword)

	rec := harness.doRequest(t, http.MethodPost, "/api/v1/model/probe", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "provider_not_configured") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestModelProbeMapsProviderFailure(t *testing.T) {
	provider := model.NewFakeProviderWithErrors(nil, map[int]error{
		1: model.NewProviderError(http.StatusTooManyRequests, "provider_rate_limit", "slow down"),
	})
	harness, _ := newGatewayHarness(t, provider, "sf-live-secret")
	token, csrf := harness.login(t, "owner@example.test", testPassword)

	rec := harness.doRequest(t, http.MethodPost, "/api/v1/model/probe", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "provider_rate_limit") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	// The audit record must carry a redacted reason, not the provider message.
	if !hasAuditAction(harness.audit, "model.probe_failed") {
		t.Fatalf("audit = %+v", harness.audit.appended)
	}
	for _, event := range harness.audit.appended {
		if event.Action == "model.probe_failed" {
			if reason, _ := event.Payload["error"].(string); reason != "provider_rate_limit" {
				t.Fatalf("audit reason = %q", reason)
			}
		}
	}
}

func TestModelGatewayDisabledWhenKeyMissing(t *testing.T) {
	gateway, err := NewModelGateway(config.ModelConfig{})
	if err != nil {
		t.Fatalf("NewModelGateway: %v", err)
	}
	if gateway.provider != nil {
		t.Fatal("gateway must be disabled without a key")
	}
	if _, err := NewModelGateway(config.ModelConfig{StepFunAPIKey: "k"}); err != nil {
		t.Fatalf("NewModelGateway with key: %v", err)
	}
}

func TestModelProbeAuditsSuccess(t *testing.T) {
	provider := model.NewFakeProvider(model.TextResponse("ready"))
	harness, _ := newGatewayHarness(t, provider, "sf-live-secret")
	token, csrf := harness.login(t, "owner@example.test", testPassword)

	harness.doRequest(t, http.MethodPost, "/api/v1/model/probe", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if !hasAuditAction(harness.audit, "model.probe") {
		t.Fatalf("audit = %+v", harness.audit.appended)
	}
}

var _ = errors.Is
var _ = httptest.NewServer

package api

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/github"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// fakeGitHubInstallations is an in-memory installation store.
type fakeGitHubInstallations struct {
	bindings []store.GitHubInstallation
	failWith error
}

func (f *fakeGitHubInstallations) Connect(_ context.Context, binding store.GitHubInstallation) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.bindings = append(f.bindings, binding)
	return nil
}

func (f *fakeGitHubInstallations) List(context.Context, string) ([]store.GitHubInstallation, error) {
	return f.bindings, nil
}

func (f *fakeGitHubInstallations) Get(_ context.Context, organizationID string, installationID int64) (store.GitHubInstallation, error) {
	for _, binding := range f.bindings {
		if binding.OrganizationID == organizationID && binding.InstallationID == installationID {
			return binding, nil
		}
	}
	return store.GitHubInstallation{}, domain.ErrNotFound
}

func (f *fakeGitHubInstallations) MarkRemoved(context.Context, string, int64) error { return nil }

type fakeWebhookDeliveries struct {
	records []store.WebhookDelivery
	seen    map[string]bool
}

func newFakeWebhookDeliveries() *fakeWebhookDeliveries {
	return &fakeWebhookDeliveries{seen: map[string]bool{}}
}

func (f *fakeWebhookDeliveries) Record(_ context.Context, delivery store.WebhookDelivery) (bool, error) {
	if f.seen[delivery.ID] {
		return true, nil
	}
	f.seen[delivery.ID] = true
	f.records = append(f.records, delivery)
	return false, nil
}

type fakeIssues struct {
	stored map[string]store.IssueRecord
}

func newFakeIssues() *fakeIssues { return &fakeIssues{stored: map[string]store.IssueRecord{}} }

func (f *fakeIssues) Import(_ context.Context, record store.IssueRecord) (store.IssueRecord, error) {
	if record.ID == "" {
		record.ID = "iss_local"
	}
	f.stored[record.ID] = record
	return record, nil
}

func (f *fakeIssues) ListForProject(context.Context, string, int, string) (store.Page[store.IssueRecord], error) {
	items := make([]store.IssueRecord, 0, len(f.stored))
	for _, record := range f.stored {
		items = append(items, record)
	}
	return store.Page[store.IssueRecord]{Items: items}, nil
}

func (f *fakeIssues) GetForProject(_ context.Context, projectID, issueID string) (store.IssueRecord, error) {
	record, ok := f.stored[issueID]
	if !ok || record.ProjectID != projectID {
		return store.IssueRecord{}, domain.ErrNotFound
	}
	return record, nil
}

func newM04Harness(t *testing.T, env config.Env, ghHandler http.HandlerFunc, secret string) (m02Harness, *fakeGitHubInstallations, *fakeWebhookDeliveries, *fakeIssues) {
	t.Helper()
	base := newM02Harness(t, env)
	base.identities.byEmail["owner@example.test"] = "hashed:" + testPassword

	ghServer := httptest.NewServer(ghHandler)
	t.Cleanup(ghServer.Close)
	adapter, err := github.NewAdapter(github.Config{
		AppID:         1,
		PrivateKeyPEM: testKeyPEM(t),
		APIBaseURL:    ghServer.URL,
		HTTPClient:    ghServer.Client(),
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	verifier, err := github.NewSignatureVerifier(secret)
	if err != nil {
		t.Fatalf("NewSignatureVerifier: %v", err)
	}
	installations := &fakeGitHubInstallations{}
	deliveries := newFakeWebhookDeliveries()
	issues := newFakeIssues()

	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: env, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          base.harnessServices(),
		Auth:              base.harnessAuth(),
		Encryptor:         base.encryptor,
		GitHubApp: &GitHubServices{
			installations: installations,
			deliveries:    deliveries,
			issues:        issues,
			projects:      &fakeProjects{getResult: domain.Project{ID: "prj_1", OrganizationID: "org_1"}},
			adapter:       adapter,
			verifier:      verifier,
		},
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	base.handler = server.Handler()
	return base, installations, deliveries, issues
}

func testKeyPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func githubHandler(t *testing.T, repos []map[string]any, issue map[string]any, comments []map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := r.URL.Path
		switch {
		case strings.Contains(path, "/repositories"):
			json.NewEncoder(w).Encode(map[string]any{"repositories": repos})
		case strings.Contains(path, "/comments"):
			json.NewEncoder(w).Encode(comments)
		case strings.Contains(path, "/issues/"):
			json.NewEncoder(w).Encode(issue)
		default:
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_test", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)})
		}
	}
}

func TestInstallationConnectAndRepoListing(t *testing.T) {
	handler := githubHandler(t,
		[]map[string]any{{"id": 1, "full_name": "acme/billing", "name": "billing", "default_branch": "main", "private": true}},
		map[string]any{"id": 9, "number": 3, "title": "Bug", "body": "text", "state": "open", "user": map[string]any{"login": "r"}},
		[]map[string]any{})
	harness, installations, _, _ := newM04Harness(t, config.EnvTest, handler, "webhook-secret")
	token, csrf := harness.login(t, "owner@example.test", testPassword)

	rec := harness.doRequest(t, http.MethodPost, "/api/v1/github/installations/connect",
		`{"installation_id":77,"account_login":"acme"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_install_1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("connect status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(installations.bindings) != 1 || installations.bindings[0].InstallationID != 77 {
		t.Fatalf("bindings = %+v", installations.bindings)
	}

	rec = harness.doRequest(t, http.MethodGet, "/api/v1/github/repos?installation_id=77", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("repos status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "acme/billing") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRepoListingDeniedForUnboundInstallation(t *testing.T) {
	handler := githubHandler(t, nil, nil, nil)
	harness, _, _, _ := newM04Harness(t, config.EnvTest, handler, "webhook-secret")
	token, _ := harness.login(t, "owner@example.test", testPassword)

	rec := harness.doRequest(t, http.MethodGet, "/api/v1/github/repos?installation_id=99", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestIssueImportNormalizesPayload(t *testing.T) {
	handler := githubHandler(t, nil,
		map[string]any{
			"id": 9, "number": 12, "title": "Pagination off by one",
			"body": "The list returns 11 items for a page of 10.", "state": "open",
			"user": map[string]any{"login": "reporter"},
		},
		[]map[string]any{{"id": 1, "body": "still reproducible", "user": map[string]any{"login": "m"}}})
	harness, _, _, issues := newM04Harness(t, config.EnvTest, handler, "webhook-secret")
	token, csrf := harness.login(t, "owner@example.test", testPassword)

	// Bind the installation first.
	harness.doRequest(t, http.MethodPost, "/api/v1/github/installations/connect",
		`{"installation_id":55}`, map[string]string{
			"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_i1"})

	rec := harness.doRequest(t, http.MethodPost, "/api/v1/issues/import",
		`{"project_id":"prj_1","installation_id":55,"issue_url":"https://github.com/acme/billing/issues/12"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_i2"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("import status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, "Pagination off by one") {
		t.Fatalf("body = %s", body)
	}
	if !strings.Contains(body, `"comments_count":1`) {
		t.Fatalf("comment count missing: %s", body)
	}
	for id, record := range issues.stored {
		if record.BodyHash == "" {
			t.Fatalf("issue %s has no body hash", id)
		}
		if record.Title != "Pagination off by one" {
			t.Fatalf("title = %q", record.Title)
		}
	}
}

func TestIssueImportRejectsMalformedURL(t *testing.T) {
	harness, _, _, _ := newM04Harness(t, config.EnvTest, githubHandler(t, nil, nil, nil), "webhook-secret")
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	for _, url := range []string{"", "not-a-url", "https://github.com/acme/billing", "https://github.com/acme/billing/issues/abc"} {
		rec := harness.doRequest(t, http.MethodPost, "/api/v1/issues/import",
			`{"project_id":"prj_1","installation_id":55,"issue_url":"`+url+`"}`,
			map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_bad"})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("url %q: status = %d, want 400", url, rec.Code)
		}
	}
}

func TestIssueImportDeniesCrossOrgProject(t *testing.T) {
	harness, _, _, _ := newM04Harness(t, config.EnvTest, githubHandler(t, nil, nil, nil), "webhook-secret")
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/issues/import",
		`{"project_id":"prj_other","installation_id":55,"issue_url":"https://github.com/acme/billing/issues/1"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_other"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func webhookBody(t *testing.T, secret string, delivery string) (string, string) {
	t.Helper()
	body := `{"action":"opened","repository":{"id":5,"full_name":"acme/billing"},"issue":{"id":9,"number":3,"title":"Bug","body":"text","state":"open"},"installation":{"id":77},"sender":{"login":"reporter"}}`
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return body, "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestWebhookAcceptsValidDeliveryAndRejectsReplay(t *testing.T) {
	harness, _, deliveries, _ := newM04Harness(t, config.EnvTest, githubHandler(t, nil, nil, nil), "webhook-secret")
	body, signature := webhookBody(t, "webhook-secret", "delivery-1")

	rec := harness.doRequest(t, http.MethodPost, "/api/v1/webhooks/github", body, map[string]string{
		github.SignatureHeader:  signature,
		github.DeliveryIDHeader: "delivery-1",
		github.EventHeader:      "issues",
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("first delivery status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(deliveries.records) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(deliveries.records))
	}

	rec = harness.doRequest(t, http.MethodPost, "/api/v1/webhooks/github", body, map[string]string{
		github.SignatureHeader:  signature,
		github.DeliveryIDHeader: "delivery-1",
		github.EventHeader:      "issues",
	})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "duplicate") {
		t.Fatalf("replay status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(deliveries.records) != 1 {
		t.Fatal("replayed delivery must not be recorded twice")
	}
}

func TestWebhookRejectsInvalidSignature(t *testing.T) {
	harness, _, deliveries, _ := newM04Harness(t, config.EnvTest, githubHandler(t, nil, nil, nil), "webhook-secret")
	body, _ := webhookBody(t, "webhook-secret", "delivery-1")

	rec := harness.doRequest(t, http.MethodPost, "/api/v1/webhooks/github", body, map[string]string{
		github.SignatureHeader:  "sha256=deadbeef",
		github.DeliveryIDHeader: "delivery-bad",
		github.EventHeader:      "issues",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(deliveries.records) != 0 {
		t.Fatal("invalid signature must not record a delivery")
	}
}

func TestWebhookAcceptsNewDeliveryIDWithSameBody(t *testing.T) {
	harness, _, deliveries, _ := newM04Harness(t, config.EnvTest, githubHandler(t, nil, nil, nil), "webhook-secret")
	body, signature := webhookBody(t, "webhook-secret", "delivery-1")

	harness.doRequest(t, http.MethodPost, "/api/v1/webhooks/github", body, map[string]string{
		github.SignatureHeader: signature, github.DeliveryIDHeader: "delivery-1", github.EventHeader: "issues"})
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/webhooks/github", body, map[string]string{
		github.SignatureHeader: signature, github.DeliveryIDHeader: "delivery-2", github.EventHeader: "issues"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("new delivery status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(deliveries.records) != 2 {
		t.Fatalf("deliveries = %d, want 2", len(deliveries.records))
	}
}

package github

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der})
}

func newTestAdapter(t *testing.T, handler http.HandlerFunc) (*Adapter, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	adapter, err := NewAdapter(Config{
		AppID:         4242,
		PrivateKeyPEM: testKey(t),
		APIBaseURL:    server.URL,
		HTTPClient:    server.Client(),
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}
	return adapter, server
}

func TestNewAdapterValidatesConfig(t *testing.T) {
	if _, err := NewAdapter(Config{PrivateKeyPEM: testKey(t)}); err == nil {
		t.Fatal("missing app id must be rejected")
	}
	if _, err := NewAdapter(Config{AppID: 1}); err == nil {
		t.Fatal("missing key must be rejected")
	}
	if _, err := NewAdapter(Config{AppID: 1, PrivateKeyPEM: []byte("not pem")}); err == nil {
		t.Fatal("invalid pem must be rejected")
	}
}

func TestAdapterListsRepositories(t *testing.T) {
	const token = "ghs_installation_token"
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "Bearer "+token {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"repositories": []map[string]any{
					{"id": 1, "full_name": "acme/billing", "name": "billing", "default_branch": "main", "private": true},
					{"id": 2, "full_name": "acme/web", "name": "web", "default_branch": "trunk", "private": false},
				},
			})
			return
		}
		// Otherwise the App JWT exchange.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"token":      token,
			"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
		})
	})

	repos, err := adapter.InstallationRepositories(context.Background(), 77)
	if err != nil {
		t.Fatalf("InstallationRepositories: %v", err)
	}
	if len(repos) != 2 || repos[0].FullName != "acme/billing" || repos[1].DefaultBranch != "trunk" {
		t.Fatalf("repos = %+v", repos)
	}
}

func TestAdapterFetchesIssueAndComments(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/comments"):
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 1, "body": "still reproducible", "created_at": time.Now().Format(time.RFC3339), "user": map[string]any{"login": "maintainer"}},
			})
		case strings.Contains(r.URL.Path, "/issues/"):
			json.NewEncoder(w).Encode(map[string]any{
				"id": 10, "number": 12, "title": "Pagination off by one",
				"body": "The list returns 11 items for a page of 10.", "state": "open",
				"created_at": time.Now().Format(time.RFC3339),
				"updated_at": time.Now().Format(time.RFC3339),
				"user":       map[string]any{"login": "reporter"},
			})
		default:
			json.NewEncoder(w).Encode(map[string]any{"token": "ghs_installation_token"})
		}
	})

	issue, err := adapter.GetIssue(context.Background(), 77, "acme", "billing", 12)
	if err != nil {
		t.Fatalf("GetIssue: %v", err)
	}
	if issue.Number != 12 || issue.Title != "Pagination off by one" {
		t.Fatalf("issue = %+v", issue)
	}
	comments, err := adapter.ListIssueComments(context.Background(), 77, "acme", "billing", 12)
	if err != nil {
		t.Fatalf("ListIssueComments: %v", err)
	}
	if len(comments) != 1 || comments[0].Body != "still reproducible" {
		t.Fatalf("comments = %+v", comments)
	}
}

func TestAdapterMapsPermissionAndRateLimitErrors(t *testing.T) {
	for _, tc := range []struct {
		status  int
		denied  bool
		limited bool
	}{
		{http.StatusForbidden, true, false},
		{http.StatusNotFound, true, false},
		{http.StatusTooManyRequests, false, true},
	} {
		adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			w.Write([]byte(`{"message":"denied"}`))
		})
		_, err := adapter.InstallationRepositories(context.Background(), 77)
		apiErr, ok := err.(*APIError)
		if !ok {
			t.Fatalf("status %d: err = %v, want *APIError", tc.status, err)
		}
		if apiErr.PermissionDenied() != tc.denied || apiErr.RateLimited() != tc.limited {
			t.Fatalf("status %d: denied=%v limited=%v", tc.status, apiErr.PermissionDenied(), apiErr.RateLimited())
		}
	}
}

func TestAppJWTIsSignedAndExpiring(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(w http.ResponseWriter, _ *http.Request) {})
	token, err := adapter.appJWT(time.Now())
	if err != nil {
		t.Fatalf("appJWT: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token parts = %d", len(parts))
	}
	claims, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	var payload struct {
		Iss int64 `json:"iss"`
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(claims, &payload); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if payload.Iss != 4242 {
		t.Fatalf("iss = %d", payload.Iss)
	}
	if payload.Exp <= time.Now().Unix() {
		t.Fatal("token must not be expired at issue time")
	}
}

// ---- webhook verification --------------------------------------------------

func signBody(t *testing.T, secret string, body []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func webhookHeaders(signature, delivery string) http.Header {
	header := http.Header{}
	header.Set(SignatureHeader, signature)
	header.Set(DeliveryIDHeader, delivery)
	header.Set(EventHeader, "issues")
	return header
}

func TestSignatureVerifierAcceptsValidDelivery(t *testing.T) {
	verifier, err := NewSignatureVerifier("webhook-secret")
	if err != nil {
		t.Fatalf("NewSignatureVerifier: %v", err)
	}
	body := []byte(`{"action":"opened"}`)
	signature := signBody(t, "webhook-secret", body)

	delivery, err := verifier.Verify(webhookHeaders(signature, "delivery-1"), body)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if delivery != "delivery-1" {
		t.Fatalf("delivery = %q", delivery)
	}
}

func TestSignatureVerifierRejectsInvalidSignature(t *testing.T) {
	verifier, _ := NewSignatureVerifier("webhook-secret")
	body := []byte(`{"action":"opened"}`)
	if _, err := verifier.Verify(webhookHeaders("sha256=deadbeef", "delivery-1"), body); err == nil {
		t.Fatal("invalid signature must be rejected")
	}
	header := webhookHeaders("", "delivery-1")
	if _, err := verifier.Verify(header, body); err == nil {
		t.Fatal("missing signature must be rejected")
	}
}

func TestSignatureVerifierRejectsReplay(t *testing.T) {
	verifier, _ := NewSignatureVerifier("webhook-secret")
	body := []byte(`{"action":"opened"}`)
	signature := signBody(t, "webhook-secret", body)

	if _, err := verifier.Verify(webhookHeaders(signature, "delivery-1"), body); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	// The same signature and delivery ID is a replay.
	if _, err := verifier.Verify(webhookHeaders(signature, "delivery-1"), body); err == nil {
		t.Fatal("replayed delivery must be rejected")
	}
	// A different delivery ID with the same body is accepted.
	if _, err := verifier.Verify(webhookHeaders(signature, "delivery-2"), body); err != nil {
		t.Fatalf("new delivery: %v", err)
	}
}

func TestSignatureVerifierRequiresDeliveryID(t *testing.T) {
	verifier, _ := NewSignatureVerifier("webhook-secret")
	body := []byte(`{"action":"opened"}`)
	header := webhookHeaders(signBody(t, "webhook-secret", body), "")
	if _, err := verifier.Verify(header, body); err == nil {
		t.Fatal("missing delivery id must be rejected")
	}
}

func TestSignatureVerifierRejectsEmptySecret(t *testing.T) {
	if _, err := NewSignatureVerifier(""); err == nil {
		t.Fatal("empty secret must be rejected")
	}
}

func TestParseEvent(t *testing.T) {
	body := []byte(`{"action":"opened","repository":{"id":5,"full_name":"acme/billing"},"issue":{"id":9,"number":3,"title":"Bug","body":"text","state":"open"},"installation":{"id":77},"sender":{"login":"reporter"}}`)
	event, action, err := ParseEvent(body)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if action != "opened" {
		t.Fatalf("action = %q", action)
	}
	if event.Repo.FullName != "acme/billing" || event.Issue == nil || event.Issue.Number != 3 {
		t.Fatalf("event = %+v", event)
	}
	if event.Installation == nil || event.Installation.ID != 77 {
		t.Fatalf("installation = %+v", event.Installation)
	}
	if _, _, err := ParseEvent([]byte("not json")); err == nil {
		t.Fatal("invalid payload must be rejected")
	}
}

func TestRetryAfterParsing(t *testing.T) {
	if RetryAfter("60") != time.Minute {
		t.Fatal("60 seconds must parse to a minute")
	}
	if RetryAfter("bogus") != 0 || RetryAfter("") != 0 {
		t.Fatal("invalid values must yield zero")
	}
}

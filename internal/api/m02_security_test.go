package api

import (
	"net/http"
	"strings"
	"testing"
)

func TestLoginSucceedsAndSetsSecureCookie(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	token, csrf := h.login(t, "owner@example.test", testPassword)
	if token == "" || csrf == "" {
		t.Fatal("login must return a session cookie and csrf token")
	}
	if len(h.sessions.created) != 1 {
		t.Fatalf("sessions created = %d, want 1", len(h.sessions.created))
	}
	rec := h.doRequest(t, http.MethodGet, "/api/v1/me", "", map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("/me status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "owner@example.test") {
		t.Fatalf("/me body = %s", rec.Body.String())
	}
}

func TestLoginRejectsWrongPasswordAndUnknownAccount(t *testing.T) {
	h := newM02Harness(t, configEnvTest)

	rec := h.doRequest(t, http.MethodPost, "/api/v1/auth/login",
		`{"email":"owner@example.test","password":"wrong-password-1234"}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password status = %d, want 401", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "invalid_credentials") {
		t.Fatalf("body = %s", body)
	}
	// The response must not reveal whether the account exists: the error code
	// and message are identical (only the per-request correlation id differs).
	rec = h.doRequest(t, http.MethodPost, "/api/v1/auth/login",
		`{"email":"nobody@example.test","password":"wrong-password-1234"}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown account status = %d, want 401", rec.Code)
	}
	if errorCode(rec) != "invalid_credentials" || errorMessage(rec) != errorMessage(firstRecorder(t, body)) {
		t.Fatalf("unknown account must be indistinguishable: %s", rec.Body.String())
	}
	if h.sessions.attempts["owner@example.test"] != 1 {
		t.Fatalf("failed attempts recorded = %d, want 1", h.sessions.attempts["owner@example.test"])
	}
}

func TestLoginRateLimitsRepeatedFailures(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	h.sessions.attempts["owner@example.test"] = maxLoginFailures

	rec := h.doRequest(t, http.MethodPost, "/api/v1/auth/login",
		`{"email":"owner@example.test","password":"test-password-1234"}`, nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "rate_limited") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if len(h.sessions.created) != 0 {
		t.Fatal("rate-limited login must not create a session")
	}
}

func TestLoginValidatesInput(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	for _, payload := range []string{
		`{"email":"","password":"valid-password-1234"}`,
		`{"email":"not-an-email","password":"valid-password-1234"}`,
		`{"email":"owner@example.test","password":"short"}`,
		`{"email":"owner@example.test","password":"valid-password-1234","extra":true}`,
	} {
		rec := h.doRequest(t, http.MethodPost, "/api/v1/auth/login", payload, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("payload %s: status = %d, want 400", payload, rec.Code)
		}
	}
}

func TestMutationRequiresCSRFToken(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	token, _ := h.login(t, "owner@example.test", testPassword)

	body := `{"organization_id":"org_1","name":"billing"}`
	rec := h.doRequest(t, http.MethodPost, "/api/v1/projects", body,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "Idempotency-Key": "idk_csrf_missing"})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("missing csrf status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "csrf_token_missing") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestMutationRejectsWrongCSRFToken(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	token, _ := h.login(t, "owner@example.test", testPassword)

	_, otherCSRF, err := newCSRFTokenForTest()
	if err != nil {
		t.Fatalf("new csrf: %v", err)
	}
	rec := h.doRequest(t, http.MethodPost, "/api/v1/projects",
		`{"organization_id":"org_1","name":"billing"}`,
		map[string]string{
			"Cookie":          sessionCookieName + "=" + token,
			"X-CSRF-Token":    otherCSRF,
			"Idempotency-Key": "idk_csrf_wrong",
		})
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "csrf_token_invalid") {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestMutationSucceedsWithCSRFToken(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	token, csrf := h.login(t, "owner@example.test", testPassword)

	rec := h.doRequest(t, http.MethodPost, "/api/v1/projects",
		`{"organization_id":"org_1","name":"billing"}`,
		map[string]string{
			"Cookie":          sessionCookieName + "=" + token,
			"X-CSRF-Token":    csrf,
			"Idempotency-Key": "idk_csrf_ok",
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestBearerRequestsSkipCSRF(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	token, _ := h.login(t, "owner@example.test", testPassword)

	rec := h.doRequest(t, http.MethodPost, "/api/v1/projects",
		`{"organization_id":"org_1","name":"billing"}`,
		map[string]string{
			"Authorization":   "Bearer " + token,
			"Idempotency-Key": "idk_bearer",
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("bearer mutation status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestLogoutRevokesSessionAndBlocksReplay(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	token, csrf := h.login(t, "owner@example.test", testPassword)

	rec := h.doRequest(t, http.MethodPost, "/api/v1/auth/logout", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", rec.Code)
	}
	if len(h.sessions.revoked) != 1 {
		t.Fatalf("revoked sessions = %d, want 1", len(h.sessions.revoked))
	}

	rec = h.doRequest(t, http.MethodGet, "/api/v1/me", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("replayed session status = %d, want 401", rec.Code)
	}
}

func TestRefreshRotatesSession(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	token, csrf := h.login(t, "owner@example.test", testPassword)

	rec := h.doRequest(t, http.MethodPost, "/api/v1/auth/refresh", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d body = %s", rec.Code, rec.Body.String())
	}
	if h.sessions.rotations != 1 {
		t.Fatalf("rotations = %d, want 1", h.sessions.rotations)
	}
	// The old token is dead.
	rec = h.doRequest(t, http.MethodGet, "/api/v1/me", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("old token status = %d, want 401", rec.Code)
	}
}

func TestProtectedRouteRequiresAuthentication(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	for _, path := range []string{"/api/v1/me", "/api/v1/members?organization_id=org_1"} {
		rec := h.doRequest(t, http.MethodGet, path, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s anonymous status = %d, want 401", path, rec.Code)
		}
	}
}

func TestCredentialValueNeverAppearsInResponses(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	token, csrf := h.login(t, "owner@example.test", testPassword)
	const secretValue = "sf-live-super-secret-value"

	rec := h.doRequest(t, http.MethodPost, "/api/v1/credentials",
		`{"organization_id":"org_1","kind":"stepfun","label":"primary","value":"`+secretValue+`"}`,
		map[string]string{
			"Cookie":          sessionCookieName + "=" + token,
			"X-CSRF-Token":    csrf,
			"Idempotency-Key": "idk_cred_1",
		})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), secretValue) {
		t.Fatal("credential value leaked in the response body")
	}
	if h.credentials.created != 1 {
		t.Fatalf("credentials created = %d", h.credentials.created)
	}
	if strings.Contains(string(h.credentials.lastBlob), secretValue) {
		t.Fatal("ciphertext contains the plaintext value")
	}

	// Rotation keeps the value hidden.
	rec = h.doRequest(t, http.MethodPost, "/api/v1/credentials/"+h.firstCredentialID()+"/rotate",
		`{"value":"sf-live-rotated-secret-value"}`,
		map[string]string{
			"Cookie":          sessionCookieName + "=" + token,
			"X-CSRF-Token":    csrf,
			"Idempotency-Key": "idk_cred_2",
		})
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate status = %d body = %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "rotated-secret") {
		t.Fatal("rotated value leaked in the response")
	}
	if !strings.Contains(rec.Body.String(), `"key_version"`) {
		t.Fatalf("rotation response must report the key version: %s", rec.Body.String())
	}

	// Revocation removes access without exposing anything.
	rec = h.doRequest(t, http.MethodDelete, "/api/v1/credentials/"+h.firstCredentialID(), "",
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204", rec.Code)
	}
	if h.credentials.revoked != 1 {
		t.Fatalf("revoked = %d, want 1", h.credentials.revoked)
	}
}

func TestCredentialAccessRequiresPermission(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	// The viewer role has only project.read.
	viewerHash, err := hashForEmail("viewer@example.test")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	h.identities.byEmail["viewer@example.test"] = viewerHash
	token, csrf := h.login(t, "viewer@example.test", testPassword)

	rec := h.doRequest(t, http.MethodPost, "/api/v1/credentials",
		`{"organization_id":"org_1","kind":"stepfun","label":"nope","value":"sf-live-should-not-store"}`,
		map[string]string{
			"Cookie":          sessionCookieName + "=" + token,
			"X-CSRF-Token":    csrf,
			"Idempotency-Key": "idk_viewer_cred",
		})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer create status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "credential.admin") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	if h.credentials.created != 0 {
		t.Fatal("viewer must not create credentials")
	}
}

func TestBootstrapIsDisabledInProduction(t *testing.T) {
	h := newM02Harness(t, configEnvProduction)
	rec := h.doRequest(t, http.MethodPost, "/api/v1/auth/bootstrap",
		`{"email":"admin@example.test","password":"bootstrap-password-1","organization_name":"Acme"}`, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "bootstrap_disabled") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestBootstrapCreatesFirstAccountAndThenRefuses(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	h.identities.byEmail = map[string]string{} // no account exists yet
	rec := h.doRequest(t, http.MethodPost, "/api/v1/auth/bootstrap",
		`{"email":"admin@example.test","password":"bootstrap-password-1","organization_name":"Acme Engineering","display_name":"Admin"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("bootstrap status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "org_") {
		t.Fatalf("body = %s", rec.Body.String())
	}

	rec = h.doRequest(t, http.MethodPost, "/api/v1/auth/bootstrap",
		`{"email":"other@example.test","password":"bootstrap-password-1","organization_name":"Other"}`, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "bootstrap_completed") {
		t.Fatalf("second bootstrap status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestUnknownSessionTreatedAsAnonymous(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	rec := h.doRequest(t, http.MethodGet, "/api/v1/me", "",
		map[string]string{"Cookie": sessionCookieName + "=not-a-real-token"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestAuditRecordsLoginAndPermissionDenials(t *testing.T) {
	h := newM02Harness(t, configEnvTest)
	_, _ = h.login(t, "owner@example.test", testPassword)
	if !hasAuditAction(h.audit, "auth.login") {
		t.Fatalf("audit actions = %v", h.audit.appended)
	}

	// A viewer attempting an admin action is audited as permission.denied.
	viewerHash, err := hashForEmail("viewer@example.test")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	h.identities.byEmail["viewer@example.test"] = viewerHash
	viewerToken, viewerCSRF := h.login(t, "viewer@example.test", testPassword)
	rec := h.doRequest(t, http.MethodPost, "/api/v1/credentials",
		`{"organization_id":"org_1","kind":"stepfun","label":"denied","value":"sf-live-denied"}`,
		map[string]string{
			"Cookie":          sessionCookieName + "=" + viewerToken,
			"X-CSRF-Token":    viewerCSRF,
			"Idempotency-Key": "idk_denied",
		})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer status = %d, want 403", rec.Code)
	}
	if !hasAuditAction(h.audit, "permission.denied") {
		t.Fatalf("permission denial not audited: %v", h.audit.appended)
	}
}

func hasAuditAction(audit *fakeAudit, action string) bool {
	for _, event := range audit.appended {
		if event.Action == action {
			return true
		}
	}
	return false
}

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/auth"
	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/secrets"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// testPassword is the shared password for the fake local identity.
const testPassword = "test-password-1234"

// hashTestPassword returns a valid argon2id hash for the shared test password.
func hashTestPassword() string {
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		panic(err)
	}
	return hash
}

// ---- auth fakes ------------------------------------------------------------

type fakeSessions struct {
	created   []fakeSessionCreate
	live      map[string]fakeSession
	rotations int
	revoked   []string
	attempts  map[string]int
}

type fakeSessionCreate struct {
	userID   string
	hash     string
	csrfHash string
}

type fakeSession struct {
	userID   string
	csrfHash string
	expires  time.Time
	revoked  bool
}

func newFakeSessions() *fakeSessions {
	return &fakeSessions{live: map[string]fakeSession{}, attempts: map[string]int{}}
}

func (f *fakeSessions) Create(_ context.Context, userID, tokenHash, csrfHash, _, _ string, _ time.Duration) (store.Session, error) {
	f.created = append(f.created, fakeSessionCreate{userID: userID, hash: tokenHash, csrfHash: csrfHash})
	f.live[tokenHash] = fakeSession{userID: userID, csrfHash: csrfHash, expires: time.Now().Add(time.Hour)}
	return store.Session{ID: tokenHash, UserID: userID, CSRFHash: csrfHash, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (f *fakeSessions) Get(_ context.Context, tokenHash string) (store.Session, error) {
	session, ok := f.live[tokenHash]
	if !ok || session.revoked || time.Now().After(session.expires) {
		return store.Session{}, domain.ErrNotFound
	}
	return store.Session{ID: tokenHash, UserID: session.userID, CSRFHash: session.csrfHash, ExpiresAt: session.expires}, nil
}

func (f *fakeSessions) Touch(context.Context, string, time.Duration) error { return nil }

func (f *fakeSessions) Rotate(_ context.Context, oldHash, newHash, newCSRF, _, _ string, _ time.Duration) (store.Session, error) {
	session, ok := f.live[oldHash]
	if !ok || session.revoked {
		return store.Session{}, domain.ErrNotFound
	}
	session.revoked = true
	f.live[oldHash] = session
	f.live[newHash] = fakeSession{userID: session.userID, csrfHash: newCSRF, expires: time.Now().Add(time.Hour)}
	f.rotations++
	return store.Session{ID: newHash, UserID: session.userID, CSRFHash: newCSRF, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (f *fakeSessions) Revoke(_ context.Context, tokenHash, _ string) error {
	session, ok := f.live[tokenHash]
	if !ok {
		return nil
	}
	session.revoked = true
	f.live[tokenHash] = session
	f.revoked = append(f.revoked, tokenHash)
	return nil
}

func (f *fakeSessions) RevokeAllForUser(context.Context, string, string) (int64, error) {
	return 0, nil
}

func (f *fakeSessions) CountRecentFailedAttempts(_ context.Context, email string, _ time.Duration) (int, error) {
	return f.attempts[email], nil
}

func (f *fakeSessions) RecordAttempt(_ context.Context, email string, succeeded bool) error {
	if succeeded {
		delete(f.attempts, email)
		return nil
	}
	f.attempts[email]++
	return nil
}

type fakeIdentities struct {
	byEmail map[string]string // email -> password hash
	users   map[string]string // email -> user id
}

func newFakeIdentities() *fakeIdentities {
	return &fakeIdentities{
		byEmail: map[string]string{},
		users: map[string]string{
			"owner@example.test":  "usr_owner",
			"viewer@example.test": "usr_viewer",
		},
	}
}

func (f *fakeIdentities) GetLocalByEmail(_ context.Context, email string) (store.Identity, error) {
	hash, ok := f.byEmail[strings.ToLower(email)]
	if !ok {
		return store.Identity{}, domain.ErrNotFound
	}
	userID := f.users[strings.ToLower(email)]
	if userID == "" {
		userID = "usr_owner"
	}
	return store.Identity{ID: "idn_" + userID, UserID: userID, Kind: "local", Subject: email, PasswordHash: hash}, nil
}

func (f *fakeIdentities) CreateLocal(_ context.Context, userID, email, passwordHash string) error {
	f.byEmail[strings.ToLower(email)] = passwordHash
	return nil
}

func (f *fakeIdentities) HasLocalIdentity(context.Context) (bool, error) {
	return len(f.byEmail) > 0, nil
}

type fakeUsers struct {
	byID   map[string]domain.User
	byMail map[string]string
}

func newFakeUsers() *fakeUsers {
	owner := domain.User{ID: "usr_owner", Email: "owner@example.test", DisplayName: "Owner"}
	viewer := domain.User{ID: "usr_viewer", Email: "viewer@example.test", DisplayName: "Viewer"}
	return &fakeUsers{
		byID:   map[string]domain.User{owner.ID: owner, viewer.ID: viewer},
		byMail: map[string]string{owner.Email: owner.ID, viewer.Email: viewer.ID},
	}
}

func (f *fakeUsers) UpsertByEmail(_ context.Context, email, displayName string) (string, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if id, ok := f.byMail[email]; ok {
		return id, nil
	}
	id := "usr_" + strings.ReplaceAll(email, "@", "_")
	f.byMail[email] = id
	f.byID[id] = domain.User{ID: id, Email: email, DisplayName: displayName}
	return id, nil
}

func (f *fakeUsers) GetByEmail(_ context.Context, email string) (domain.User, error) {
	id, ok := f.byMail[strings.ToLower(strings.TrimSpace(email))]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return f.byID[id], nil
}

func (f *fakeUsers) Get(_ context.Context, userID string) (domain.User, error) {
	user, ok := f.byID[userID]
	if !ok {
		return domain.User{}, domain.ErrNotFound
	}
	return user, nil
}

type fakeMembers struct {
	roles       map[string]string // orgID/userID -> role
	permissions map[string][]string
}

func newFakeMembers() *fakeMembers {
	return &fakeMembers{
		roles:       map[string]string{"org_1/usr_owner": domain.RoleOwner, "org_1/usr_viewer": domain.RoleViewer},
		permissions: map[string][]string{"org_1/usr_owner": {"project.read", "project.write", "run.execute", "credential.admin", "member.admin", "approval.decide", "merge.execute"}, "org_1/usr_viewer": {"project.read"}},
	}
}

func (f *fakeMembers) List(context.Context, string, int) (store.Page[store.Member], error) {
	return store.Page[store.Member]{Items: []store.Member{}}, nil
}
func (f *fakeMembers) CountOwners(context.Context, string) (int, error) { return 1, nil }
func (f *fakeMembers) Get(context.Context, string, string) (store.Member, error) {
	return store.Member{}, domain.ErrNotFound
}
func (f *fakeMembers) Invite(context.Context, string, string, string, string) (store.Member, error) {
	return store.Member{}, nil
}
func (f *fakeMembers) UpdateRole(context.Context, string, string, string) (store.Member, error) {
	return store.Member{}, nil
}
func (f *fakeMembers) Remove(context.Context, string, string) error { return nil }
func (f *fakeMembers) PermissionsFor(_ context.Context, organizationID, userID string) ([]string, error) {
	return f.permissions[organizationID+"/"+userID], nil
}
func (f *fakeMembers) RoleFor(_ context.Context, organizationID, userID string) (string, error) {
	role, ok := f.roles[organizationID+"/"+userID]
	if !ok {
		return "", domain.ErrNotFound
	}
	return role, nil
}

type fakeOrgs struct {
	// userOrgs maps a user id to the organizations they belong to; the first
	// entry becomes the subject's active organization.
	userOrgs map[string][]domain.OrganizationWithRole
}

// withUserOrg wires a user to a specific set of organizations.
func (f *fakeOrgs) withUserOrg(userID, orgID string) *fakeOrgs {
	if f.userOrgs == nil {
		f.userOrgs = map[string][]domain.OrganizationWithRole{}
	}
	f.userOrgs[userID] = append(f.userOrgs[userID], domain.OrganizationWithRole{
		Organization: domain.Organization{ID: orgID, Name: "Org " + orgID, Slug: orgID},
		Role:         domain.RoleOwner,
	})
	return f
}

func (fakeOrgs) Create(context.Context, domain.Organization, string, string) error { return nil }
func (fakeOrgs) Get(context.Context, string) (domain.Organization, error) {
	return domain.Organization{}, domain.ErrNotFound
}
func (fakeOrgs) GetBySlug(context.Context, string) (domain.Organization, error) {
	return domain.Organization{}, domain.ErrNotFound
}
func (f fakeOrgs) ListForUser(_ context.Context, userID string, _ int, _ string) (domain.Page[domain.OrganizationWithRole], error) {
	if orgs, ok := f.userOrgs[userID]; ok {
		return domain.Page[domain.OrganizationWithRole]{Items: orgs}, nil
	}
	return domain.Page[domain.OrganizationWithRole]{Items: []domain.OrganizationWithRole{{Organization: domain.Organization{ID: "org_1", Name: "Acme", Slug: "acme"}, Role: domain.RoleOwner}}}, nil
}
func (fakeOrgs) Membership(_ context.Context, organizationID, userID string) (string, error) {
	if organizationID == "org_1" && userID == "usr_owner" {
		return domain.RoleOwner, nil
	}
	return "", domain.ErrNotFound
}
func (fakeOrgs) SlugTaken(context.Context, string) (bool, error) { return false, nil }

type fakeCredentials struct {
	items    map[string]store.Credential
	created  int
	rotated  int
	revoked  int
	lastBlob []byte
}

func newFakeCredentials() *fakeCredentials {
	return &fakeCredentials{items: map[string]store.Credential{}}
}

func (f *fakeCredentials) Create(_ context.Context, credential store.Credential) error {
	f.items[credential.ID] = credential
	f.created++
	f.lastBlob = credential.Ciphertext
	return nil
}
func (f *fakeCredentials) Get(_ context.Context, organizationID, credentialID string) (store.Credential, error) {
	credential, ok := f.items[credentialID]
	if !ok || credential.OrganizationID != organizationID {
		return store.Credential{}, domain.ErrNotFound
	}
	return credential, nil
}
func (f *fakeCredentials) List(context.Context, string, int, string) (store.Page[store.Credential], error) {
	return store.Page[store.Credential]{}, nil
}
func (f *fakeCredentials) Rotate(_ context.Context, organizationID, credentialID string, ciphertext, nonce []byte, keyVersion int, fingerprint string) error {
	credential, ok := f.items[credentialID]
	if !ok || credential.OrganizationID != organizationID {
		return domain.ErrNotFound
	}
	credential.Ciphertext = ciphertext
	credential.Nonce = nonce
	credential.KeyVersion = keyVersion
	credential.Fingerprint = fingerprint
	f.items[credentialID] = credential
	f.rotated++
	f.lastBlob = ciphertext
	return nil
}
func (f *fakeCredentials) Revoke(_ context.Context, organizationID, credentialID string) error {
	credential, ok := f.items[credentialID]
	if !ok || credential.OrganizationID != organizationID {
		return domain.ErrNotFound
	}
	credential.Status = "revoked"
	f.items[credentialID] = credential
	f.revoked++
	return nil
}
func (f *fakeCredentials) MarkVerified(context.Context, string, string) error { return nil }

// ---- harness ---------------------------------------------------------------

type m02Harness struct {
	handler     http.Handler
	fakeRuns    *fakeRunsStore
	fakeQueue   *fakeTaskQueue
	sessions    *fakeSessions
	identities  *fakeIdentities
	members     *fakeMembers
	credentials *fakeCredentials
	users       *fakeUsers
	audit       *fakeAudit
	encryptor   *secrets.Encryptor
}

func newM02Harness(t *testing.T, env config.Env) m02Harness {
	t.Helper()
	sessions := newFakeSessions()
	identities := newFakeIdentities()
	members := newFakeMembers()
	credentials := newFakeCredentials()
	users := newFakeUsers()
	audit := &fakeAudit{}
	idem := newFakeIdempotency()
	encryptor, err := secrets.New("WlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlpaWlo=", 1)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	identities.byEmail["owner@example.test"] = hashTestPassword()

	srv, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: env, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services: &Services{
			Organizations: fakeOrgs{},
			Projects:      &fakeProjects{},
			Audit:         audit,
			Idempotency:   idem,
		},
		Auth: NewAuthServices(AuthServicesConfig{
			Sessions:      sessions,
			Identities:    identities,
			Members:       members,
			Users:         users,
			Organizations: fakeOrgs{},
			Credentials:   credentials,
		}),
		Encryptor: encryptor,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return m02Harness{
		handler:     srv.Handler(),
		sessions:    sessions,
		identities:  identities,
		members:     members,
		credentials: credentials,
		users:       users,
		audit:       audit,
		encryptor:   encryptor,
	}
}

// doRequest performs a request and returns the recorder.
func (h m02Harness) doRequest(t *testing.T, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

// login performs a real login and returns the session token and CSRF token.
func (h m02Harness) login(t *testing.T, email, password string) (token, csrf string) {
	t.Helper()
	rec := h.doRequest(t, http.MethodPost, "/api/v1/auth/login",
		`{"email":"`+email+`","password":"`+password+`"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == sessionCookieName {
			token = cookie.Value
		}
	}
	if token == "" {
		t.Fatal("login did not set a session cookie")
	}
	return token, payload.CSRFToken
}

// configEnvTest / configEnvProduction select the environment for harnesses.
const (
	configEnvTest       = config.EnvTest
	configEnvProduction = config.EnvProduction
)

// newCSRFTokenForTest mints a CSRF token pair for tests.
func newCSRFTokenForTest() (string, string, error) {
	return auth.NewCSRFToken()
}

// hashForEmail stores and returns a hash for another fake identity.
func hashForEmail(email string) (string, error) {
	return auth.HashPassword(testPassword)
}

// errorCode extracts the canonical error code from a response.
func errorCode(rec *httptest.ResponseRecorder) string {
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		return ""
	}
	return payload.Error.Code
}

// errorMessage extracts the canonical error message from a response.
func errorMessage(rec *httptest.ResponseRecorder) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		return ""
	}
	return payload.Error.Message
}

// firstRecorder wraps a stored body so tests can compare messages.
func firstRecorder(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	_, _ = rec.WriteString(body)
	return rec
}

// firstCredentialID returns the only credential in the fake store.
func (h m02Harness) firstCredentialID() string {
	for id := range h.credentials.items {
		return id
	}
	return ""
}

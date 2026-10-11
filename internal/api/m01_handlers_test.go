package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
)

// ---- fakes -----------------------------------------------------------------

type fakeOrganizations struct {
	createErr     error
	getResult     domain.Organization
	getErr        error
	listResult    domain.Page[domain.OrganizationWithRole]
	listErr       error
	memberships   map[string]string
	membershipErr error
	slugTaken     bool
	slugTakenErr  error

	createCalls   int
	listUserCalls int
}

func (f *fakeOrganizations) Create(_ context.Context, _ domain.Organization, _, _ string) error {
	f.createCalls++
	return f.createErr
}

func (f *fakeOrganizations) Get(_ context.Context, _ string) (domain.Organization, error) {
	return f.getResult, f.getErr
}

func (f *fakeOrganizations) GetBySlug(_ context.Context, _ string) (domain.Organization, error) {
	return domain.Organization{}, domain.ErrNotFound
}

func (f *fakeOrganizations) ListForUser(_ context.Context, _ string, _ int, _ string) (domain.Page[domain.OrganizationWithRole], error) {
	f.listUserCalls++
	return f.listResult, f.listErr
}

func (f *fakeOrganizations) Membership(_ context.Context, organizationID, userID string) (string, error) {
	if f.membershipErr != nil {
		return "", f.membershipErr
	}
	role, ok := f.memberships[organizationID+"/"+userID]
	if !ok {
		return "", domain.ErrNotFound
	}
	return role, nil
}

func (f *fakeOrganizations) SlugTaken(_ context.Context, _ string) (bool, error) {
	return f.slugTaken, f.slugTakenErr
}

type fakeProjects struct {
	createErr    error
	getResult    domain.Project
	getErr       error
	listResult   domain.Page[domain.Project]
	listErr      error
	updateResult domain.Project
	updateErr    error

	createCalls int
	updateCalls int
	lastUpdate  updateArgs
}

type updateArgs struct {
	organizationID string
	projectID      string
	version        int64
	update         domain.ProjectUpdate
	actorID        string
}

func (f *fakeProjects) Create(_ context.Context, _ domain.Project, _ string) error {
	f.createCalls++
	return f.createErr
}

func (f *fakeProjects) GetForOrg(_ context.Context, organizationID, projectID string) (domain.Project, error) {
	if f.getErr != nil {
		return domain.Project{}, f.getErr
	}
	// Unknown organizations and unknown project ids are both not found, so
	// handlers that scope by project cannot be tested with a blanket stub.
	if organizationID != "" && f.getResult.OrganizationID != "" && organizationID != f.getResult.OrganizationID {
		return domain.Project{}, domain.ErrNotFound
	}
	if projectID != "" && f.getResult.ID != "" && projectID != f.getResult.ID {
		return domain.Project{}, domain.ErrNotFound
	}
	return f.getResult, nil
}

func (f *fakeProjects) ListForOrg(_ context.Context, _, _ string, _ int, _ string) (domain.Page[domain.Project], error) {
	return f.listResult, f.listErr
}

func (f *fakeProjects) UpdateForOrg(_ context.Context, organizationID, projectID string, version int64, update domain.ProjectUpdate, actorID string) (domain.Project, error) {
	f.updateCalls++
	f.lastUpdate = updateArgs{organizationID: organizationID, projectID: projectID, version: version, update: update, actorID: actorID}
	return f.updateResult, f.updateErr
}

type fakeAudit struct {
	appended   []domain.AuditEvent
	appendErr  error
	listResult domain.Page[domain.AuditEvent]
	listErr    error
}

func (f *fakeAudit) Append(_ context.Context, events []domain.AuditEvent) error {
	f.appended = append(f.appended, events...)
	return f.appendErr
}

func (f *fakeAudit) ListForProject(_ context.Context, _, _, _ string, _ int, _ string) (domain.Page[domain.AuditEvent], error) {
	return f.listResult, f.listErr
}

type fakeIdempotency struct {
	records   map[string]map[string]any
	getErr    error
	storeErr  error
	storeKeys []string
}

func newFakeIdempotency() *fakeIdempotency {
	return &fakeIdempotency{records: map[string]map[string]any{}}
}

func (f *fakeIdempotency) Get(_ context.Context, key string) (int, map[string]any, bool, error) {
	if f.getErr != nil {
		return 0, nil, false, f.getErr
	}
	record, ok := f.records[key]
	if !ok {
		return 0, nil, false, nil
	}
	status, _ := record["status"].(int)
	return status, record, true, nil
}

func (f *fakeIdempotency) Store(_ context.Context, key, _, _, _ string, status int, body map[string]any, _ time.Duration) error {
	if f.storeErr != nil {
		return f.storeErr
	}
	f.records[key] = body
	f.storeKeys = append(f.storeKeys, key)
	return nil
}

// ---- harness ---------------------------------------------------------------

// newM01Handler builds the full M01 handler chain with the development subject
// resolver installed, mirroring cmd/api wiring.
func newM01Handler(t *testing.T, env config.Env, services *Services) http.Handler {
	t.Helper()
	cfg := &config.Config{
		Process:  config.ProcessAPI,
		Env:      env,
		HTTPAddr: ":8080",
	}
	srv, err := NewServer(Options{
		Config: cfg,
		DB:     pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) {
			return nil, nil
		},
		Services: services,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return subjectMiddleware(devSubjectResolver{env: env}, srv.Handler())
}

func doJSON(t *testing.T, handler http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode body %q: %v", rec.Body.String(), err)
	}
	return out
}

func idempotencyHeaders(extra map[string]string) map[string]string {
	headers := map[string]string{"Idempotency-Key": "idk_" + strings.Repeat("a", 20)}
	for k, v := range extra {
		headers[k] = v
	}
	return headers
}

// ---- organizations ---------------------------------------------------------

func TestCreateOrganizationSucceedsAndAudits(t *testing.T) {
	orgs := &fakeOrganizations{}
	audit := &fakeAudit{}
	idem := newFakeIdempotency()
	orgs.getResult = domain.Organization{ID: "org_1", Slug: "acme", Name: "Acme", CreatedAt: time.Now().UTC()}
	handler := newM01Handler(t, config.EnvTest, &Services{Organizations: orgs, Audit: audit, Idempotency: idem, Projects: &fakeProjects{}})

	rec := doJSON(t, handler, http.MethodPost, "/api/v1/organizations",
		`{"name":"Acme","slug":"acme"}`, idempotencyHeaders(nil))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if orgs.createCalls != 1 {
		t.Fatalf("create calls = %d", orgs.createCalls)
	}
	body := decodeBody(t, rec)
	if body["slug"] != "acme" || body["role"] != domain.RoleOwner {
		t.Fatalf("body = %v", body)
	}
	if len(audit.appended) != 1 || audit.appended[0].Action != "organization.created" {
		t.Fatalf("audit events = %+v", audit.appended)
	}
	if len(idem.storeKeys) != 1 {
		t.Fatalf("idempotency keys stored = %d", len(idem.storeKeys))
	}
}

func TestCreateOrganizationRequiresIdempotencyKey(t *testing.T) {
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{}, Audit: &fakeAudit{}, Idempotency: newFakeIdempotency(), Projects: &fakeProjects{},
	})
	rec := doJSON(t, handler, http.MethodPost, "/api/v1/organizations", `{"name":"Acme","slug":"acme"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	body := decodeBody(t, rec)
	errorBody, _ := body["error"].(map[string]any)
	if errorBody["code"] != "idempotency_required" {
		t.Fatalf("body = %v", body)
	}
}

func TestCreateOrganizationValidatesSlug(t *testing.T) {
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{}, Audit: &fakeAudit{}, Idempotency: newFakeIdempotency(), Projects: &fakeProjects{},
	})
	for i, slug := range []string{"A", "has space", "-leading", "trailing-", "x"} {
		key := fmt.Sprintf("idk_slug_%02d_%s", i, strings.Repeat("a", 20))
		rec := doJSON(t, handler, http.MethodPost, "/api/v1/organizations",
			`{"name":"Acme","slug":"`+slug+`"}`, idempotencyHeaders(map[string]string{"Idempotency-Key": key}))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("slug %q: status = %d, want 400", slug, rec.Code)
		}
	}
}

func TestCreateOrganizationRejectsDuplicateSlug(t *testing.T) {
	orgs := &fakeOrganizations{slugTaken: true}
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: orgs, Audit: &fakeAudit{}, Idempotency: newFakeIdempotency(), Projects: &fakeProjects{},
	})
	rec := doJSON(t, handler, http.MethodPost, "/api/v1/organizations",
		`{"name":"Acme","slug":"acme"}`, idempotencyHeaders(nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	body := decodeBody(t, rec)
	errorBody, _ := body["error"].(map[string]any)
	if errorBody["code"] != domain.ConflictDuplicateName {
		t.Fatalf("body = %v", body)
	}
}

func TestListOrganizationsRequiresSubject(t *testing.T) {
	orgs := &fakeOrganizations{listResult: domain.Page[domain.OrganizationWithRole]{Items: []domain.OrganizationWithRole{}}}
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: orgs, Audit: &fakeAudit{}, Idempotency: newFakeIdempotency(), Projects: &fakeProjects{},
	})
	// Development resolver always resolves a subject.
	rec := doJSON(t, handler, http.MethodGet, "/api/v1/organizations", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if orgs.listUserCalls != 1 {
		t.Fatalf("list calls = %d", orgs.listUserCalls)
	}
}

func TestListOrganizationsRejectedInProduction(t *testing.T) {
	handler := newM01Handler(t, config.EnvProduction, &Services{
		Organizations: &fakeOrganizations{}, Audit: &fakeAudit{}, Idempotency: newFakeIdempotency(), Projects: &fakeProjects{},
	})
	rec := doJSON(t, handler, http.MethodGet, "/api/v1/organizations", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	body := decodeBody(t, rec)
	errorBody, _ := body["error"].(map[string]any)
	if errorBody["code"] != "unauthenticated" {
		t.Fatalf("body = %v", body)
	}
}

// ---- projects --------------------------------------------------------------

func TestCreateProjectSucceedsAndAudits(t *testing.T) {
	projects := &fakeProjects{}
	audit := &fakeAudit{}
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{}, Projects: projects, Audit: audit, Idempotency: newFakeIdempotency(),
	})
	rec := doJSON(t, handler, http.MethodPost, "/api/v1/projects",
		`{"organization_id":"org_1","name":"billing","description":"payments"}`,
		idempotencyHeaders(nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if projects.createCalls != 1 {
		t.Fatalf("create calls = %d", projects.createCalls)
	}
	if len(audit.appended) != 1 || audit.appended[0].Action != "project.created" {
		t.Fatalf("audit events = %+v", audit.appended)
	}
	body := decodeBody(t, rec)
	if body["name"] != "billing" || body["version"].(float64) != 0 {
		t.Fatalf("body = %v", body)
	}
}

func TestCreateProjectSurfacesConflict(t *testing.T) {
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{},
		Projects:      &fakeProjects{createErr: domain.NewDuplicateName("project", "billing")},
		Audit:         &fakeAudit{},
		Idempotency:   newFakeIdempotency(),
	})
	rec := doJSON(t, handler, http.MethodPost, "/api/v1/projects",
		`{"organization_id":"org_1","name":"billing"}`, idempotencyHeaders(nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	body := decodeBody(t, rec)
	errorBody, _ := body["error"].(map[string]any)
	if errorBody["code"] != domain.ConflictDuplicateName {
		t.Fatalf("body = %v", body)
	}
}

func TestCreateProjectSurfacesPermissionDenied(t *testing.T) {
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{},
		Projects:      &fakeProjects{createErr: domain.ErrPermissionDenied},
		Audit:         &fakeAudit{},
		Idempotency:   newFakeIdempotency(),
	})
	rec := doJSON(t, handler, http.MethodPost, "/api/v1/projects",
		`{"organization_id":"org_1","name":"billing"}`, idempotencyHeaders(nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	body := decodeBody(t, rec)
	errorBody, _ := body["error"].(map[string]any)
	if errorBody["code"] != "permission_denied" {
		t.Fatalf("body = %v", body)
	}
}

func TestCreateProjectValidatesFields(t *testing.T) {
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{}, Projects: &fakeProjects{}, Audit: &fakeAudit{}, Idempotency: newFakeIdempotency(),
	})
	rec := doJSON(t, handler, http.MethodPost, "/api/v1/projects",
		`{"organization_id":"","name":""}`, idempotencyHeaders(nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	body := decodeBody(t, rec)
	errorBody, _ := body["error"].(map[string]any)
	details, _ := errorBody["details"].(map[string]any)
	fields, _ := details["fields"].(map[string]any)
	if _, ok := fields["name"]; !ok {
		t.Fatalf("fields = %v", fields)
	}
}

func TestCreateProjectRejectsUnknownFields(t *testing.T) {
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{}, Projects: &fakeProjects{}, Audit: &fakeAudit{}, Idempotency: newFakeIdempotency(),
	})
	rec := doJSON(t, handler, http.MethodPost, "/api/v1/projects",
		`{"organization_id":"org_1","name":"billing","role":"owner"}`, idempotencyHeaders(nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestListProjectsRequiresOrganizationAndMembership(t *testing.T) {
	orgs := &fakeOrganizations{}
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: orgs, Projects: &fakeProjects{}, Audit: &fakeAudit{}, Idempotency: newFakeIdempotency(),
	})

	rec := doJSON(t, handler, http.MethodGet, "/api/v1/projects?organization_id=org_1", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-member status = %d, want 404", rec.Code)
	}

	orgs.memberships = map[string]string{"org_1/usr_development": domain.RoleOwner}
	orgs.listResult = domain.Page[domain.OrganizationWithRole]{Items: []domain.OrganizationWithRole{}}
	projects := &fakeProjects{listResult: domain.Page[domain.Project]{}}
	handler = newM01Handler(t, config.EnvTest, &Services{
		Organizations: orgs, Projects: projects, Audit: &fakeAudit{}, Idempotency: newFakeIdempotency(),
	})
	rec = doJSON(t, handler, http.MethodGet, "/api/v1/projects?organization_id=org_1&q=bil", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("member status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestGetProjectUsesActiveOrganization(t *testing.T) {
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{},
		Projects:      &fakeProjects{getResult: domain.Project{ID: "prj_1", OrganizationID: "org_1", Name: "billing"}},
		Audit:         &fakeAudit{},
		Idempotency:   newFakeIdempotency(),
	})
	rec := doJSON(t, handler, http.MethodGet, "/api/v1/projects/prj_1", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["id"] != "prj_1" {
		t.Fatalf("body = %v", body)
	}
}

func TestGetProjectForeignIDIsNotFound(t *testing.T) {
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{},
		Projects:      &fakeProjects{getErr: domain.ErrNotFound},
		Audit:         &fakeAudit{},
		Idempotency:   newFakeIdempotency(),
	})
	rec := doJSON(t, handler, http.MethodGet, "/api/v1/projects/prj_foreign", "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestUpdateProjectVersionConflictReturnsCurrentVersion(t *testing.T) {
	projects := &fakeProjects{
		updateErr: domain.NewVersionConflict(7),
	}
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{},
		Projects:      projects,
		Audit:         &fakeAudit{},
		Idempotency:   newFakeIdempotency(),
	})
	rec := doJSON(t, handler, http.MethodPatch, "/api/v1/projects/prj_1",
		`{"version":3,"description":"updated"}`,
		idempotencyHeaders(nil))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	body := decodeBody(t, rec)
	errorBody, _ := body["error"].(map[string]any)
	if errorBody["code"] != domain.ConflictVersionMismatch {
		t.Fatalf("body = %v", body)
	}
	details, _ := errorBody["details"].(map[string]any)
	if details["current_version"].(float64) != 7 {
		t.Fatalf("details = %v", details)
	}
	if projects.lastUpdate.version != 3 {
		t.Fatalf("passed version = %d, want 3", projects.lastUpdate.version)
	}
}

func TestUpdateProjectSucceeds(t *testing.T) {
	projects := &fakeProjects{updateResult: domain.Project{
		ID: "prj_1", OrganizationID: "org_1", Name: "billing", Version: 4,
	}}
	audit := &fakeAudit{}
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{},
		Projects:      projects,
		Audit:         audit,
		Idempotency:   newFakeIdempotency(),
	})
	rec := doJSON(t, handler, http.MethodPatch, "/api/v1/projects/prj_1",
		`{"version":3,"description":"updated","settings":{"default_branch":"main"}}`,
		idempotencyHeaders(nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	if body["version"].(float64) != 4 {
		t.Fatalf("body = %v", body)
	}
	if len(audit.appended) != 1 || audit.appended[0].Action != "project.updated" {
		t.Fatalf("audit = %+v", audit.appended)
	}
}

func TestUpdateProjectRejectsUnknownSettingsKey(t *testing.T) {
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{},
		Projects:      &fakeProjects{},
		Audit:         &fakeAudit{},
		Idempotency:   newFakeIdempotency(),
	})
	rec := doJSON(t, handler, http.MethodPatch, "/api/v1/projects/prj_1",
		`{"version":3,"settings":{"smuggled":"value"}}`, idempotencyHeaders(nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// ---- audit -----------------------------------------------------------------

func TestListAuditRequiresProjectID(t *testing.T) {
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{}, Projects: &fakeProjects{}, Audit: &fakeAudit{}, Idempotency: newFakeIdempotency(),
	})
	rec := doJSON(t, handler, http.MethodGet, "/api/v1/audit", "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestListAuditReturnsEvents(t *testing.T) {
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{},
		Projects:      &fakeProjects{getResult: domain.Project{ID: "prj_1", OrganizationID: "org_1"}},
		Audit: &fakeAudit{listResult: domain.Page[domain.AuditEvent]{
			Items: []domain.AuditEvent{{ID: "aud_1", Action: "project.created", EntityType: "project", Sequence: 4}},
		}},
		Idempotency: newFakeIdempotency(),
	})
	rec := doJSON(t, handler, http.MethodGet, "/api/v1/audit?project_id=prj_1", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := decodeBody(t, rec)
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", body["items"])
	}
}

// ---- idempotency -----------------------------------------------------------

func TestIdempotentReplayReturnsSameResponse(t *testing.T) {
	projects := &fakeProjects{}
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{},
		Projects:      projects,
		Audit:         &fakeAudit{},
		Idempotency:   newFakeIdempotency(),
	})
	headers := idempotencyHeaders(nil)
	first := doJSON(t, handler, http.MethodPost, "/api/v1/projects",
		`{"organization_id":"org_1","name":"billing"}`, headers)
	if first.Code != http.StatusCreated {
		t.Fatalf("first status = %d", first.Code)
	}
	second := doJSON(t, handler, http.MethodPost, "/api/v1/projects",
		`{"organization_id":"org_1","name":"billing"}`, headers)
	if second.Code != http.StatusCreated {
		t.Fatalf("second status = %d", second.Code)
	}
	if projects.createCalls != 1 {
		t.Fatalf("create calls = %d, want 1", projects.createCalls)
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replayed body differs:\n%s\n%s", first.Body.String(), second.Body.String())
	}
}

func TestIdempotencyKeyReuseWithDifferentBodyIsRejected(t *testing.T) {
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{},
		Projects:      &fakeProjects{},
		Audit:         &fakeAudit{},
		Idempotency:   newFakeIdempotency(),
	})
	headers := idempotencyHeaders(nil)
	if rec := doJSON(t, handler, http.MethodPost, "/api/v1/projects",
		`{"organization_id":"org_1","name":"billing"}`, headers); rec.Code != http.StatusCreated {
		t.Fatalf("first status = %d", rec.Code)
	}
	rec := doJSON(t, handler, http.MethodPost, "/api/v1/projects",
		`{"organization_id":"org_1","name":"other"}`, headers)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	body := decodeBody(t, rec)
	errorBody, _ := body["error"].(map[string]any)
	if errorBody["code"] != "idempotency_key_reuse" {
		t.Fatalf("body = %v", body)
	}
}

func TestFailedRequestIsNotRecordedForReplay(t *testing.T) {
	idem := newFakeIdempotency()
	handler := newM01Handler(t, config.EnvTest, &Services{
		Organizations: &fakeOrganizations{},
		Projects:      &fakeProjects{createErr: domain.ErrPermissionDenied},
		Audit:         &fakeAudit{},
		Idempotency:   idem,
	})
	headers := idempotencyHeaders(nil)
	if rec := doJSON(t, handler, http.MethodPost, "/api/v1/projects",
		`{"organization_id":"org_1","name":"billing"}`, headers); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if len(idem.storeKeys) != 0 {
		t.Fatalf("failures must not be recorded: %v", idem.storeKeys)
	}
}

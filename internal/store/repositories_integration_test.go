package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
)

func TestOrganizationCreateAndMembership(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	fixture := env.seedOrganization(t, ctx)

	role, err := env.organizations.Membership(ctx, fixture.organization.ID, fixture.ownerID)
	if err != nil {
		t.Fatalf("Membership: %v", err)
	}
	if role != domain.RoleOwner {
		t.Fatalf("role = %q, want %q", role, domain.RoleOwner)
	}

	stored, err := env.organizations.Get(ctx, fixture.organization.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Slug != fixture.organization.Slug || stored.Name != fixture.organization.Name {
		t.Fatalf("stored = %+v, want %+v", stored, fixture.organization)
	}

	// Unknown slugs are reported as not found so pre-checks can act on them.
	if _, err := env.organizations.GetBySlug(ctx, "slug-"+ids.MustNew("org")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("GetBySlug(unknown) err = %v, want ErrNotFound", err)
	}
}

func TestOrganizationCreateRollsBackWhenRoleInsertFails(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()

	orgID := ids.MustNew("org")
	err := env.organizations.Create(ctx, domain.Organization{
		ID:   orgID,
		Slug: "org-" + orgID,
		Name: "Broken membership",
	}, "usr_does_not_exist", domain.RoleOwner)
	if err == nil {
		t.Fatal("expected foreign key failure for unknown actor")
	}
	var count int64
	if count = env.countInt(ctx, t, `SELECT count(*) FROM organizations WHERE id = $1`, orgID); count != 0 {
		t.Fatalf("organization survived the failed transaction: %d rows", count)
	}
}

func TestOrganizationRejectsDuplicateSlug(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	first := env.seedOrganization(t, ctx)

	secondUser, err := env.users.UpsertByEmail(ctx, "user-"+ids.MustNew("usr")+"@example.test", "Second")
	if err != nil {
		t.Fatalf("seed second user: %v", err)
	}
	err = env.organizations.Create(ctx, domain.Organization{
		ID:   ids.MustNew("org"),
		Slug: first.organization.Slug,
		Name: "Colliding slug",
	}, secondUser, domain.RoleOwner)
	if err == nil {
		t.Fatal("expected duplicate slug to fail")
	}
}

func TestOrganizationListForUserPaginates(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()

	userID, err := env.users.UpsertByEmail(ctx, "user-"+ids.MustNew("usr")+"@example.test", "Pager")
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	const total = 25
	for i := 0; i < total; i++ {
		org := domain.Organization{
			ID:   ids.MustNew("org"),
			Slug: "pager-" + ids.MustNew("org"),
			Name: fmt.Sprintf("Pager org %02d", i),
		}
		if err := env.organizations.Create(ctx, org, userID, domain.RoleOwner); err != nil {
			t.Fatalf("create org %d: %v", i, err)
		}
		// Stagger creation so ordering is deterministic without sleeping long.
		time.Sleep(2 * time.Millisecond)
	}

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		page, err := env.organizations.ListForUser(ctx, userID, 10, cursor)
		if err != nil {
			t.Fatalf("ListForUser (page %d): %v", pages+1, err)
		}
		if len(page.Items) == 0 {
			t.Fatal("page returned no items")
		}
		for _, org := range page.Items {
			if seen[org.ID] {
				t.Fatalf("organization %s returned twice", org.ID)
			}
			seen[org.ID] = true
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if len(seen) < total {
		t.Fatalf("saw %d organizations across %d pages, want at least %d", len(seen), pages, total)
	}
}

func TestOrganizationListRejectsTamperedCursor(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	fixture := env.seedOrganization(t, ctx)

	_, err := env.organizations.ListForUser(ctx, fixture.ownerID, 10, "not-a-real-cursor")
	if err == nil || !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestCrossTenantProjectIsolation(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()

	orgA := env.seedOrganization(t, ctx)
	orgB := env.seedOrganization(t, ctx)

	projectA := env.seedProject(t, ctx, orgA, "isolation-a")

	if _, err := env.projects.GetForOrg(ctx, orgB.organization.ID, projectA.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cross-tenant read err = %v, want ErrNotFound", err)
	}

	pageB, err := env.projects.ListForOrg(ctx, orgB.organization.ID, "", 50, "")
	if err != nil {
		t.Fatalf("ListForOrg: %v", err)
	}
	for _, project := range pageB.Items {
		if project.ID == projectA.ID {
			t.Fatal("cross-tenant project leaked into org B listing")
		}
	}

	role, err := env.organizations.Membership(ctx, orgB.organization.ID, orgA.ownerID)
	if !errors.Is(err, domain.ErrNotFound) || role != "" {
		t.Fatalf("membership = %q err = %v, want ErrNotFound", role, err)
	}

	// Audit rows follow the same rule.
	pageAuditB, err := env.audit.ListForProject(ctx, orgB.organization.ID, projectA.ID, "", 50, "")
	if err != nil {
		t.Fatalf("ListForProject: %v", err)
	}
	if len(pageAuditB.Items) != 0 {
		t.Fatalf("cross-tenant audit rows leaked: %d", len(pageAuditB.Items))
	}
}

func TestProjectCreateRequiresWriteRole(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	org := env.seedOrganization(t, ctx)

	// A non-member cannot create projects.
	err := env.projects.Create(ctx, domain.Project{
		ID:             ids.MustNew("prj"),
		OrganizationID: org.organization.ID,
		Name:           "outsider",
	}, ids.MustNew("usr"))
	if !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("err = %v, want ErrPermissionDenied", err)
	}

	// A viewer member is read-only.
	viewerID, err := env.users.UpsertByEmail(ctx, "user-"+ids.MustNew("usr")+"@example.test", "Viewer")
	if err != nil {
		t.Fatalf("seed viewer: %v", err)
	}
	env.exec(ctx, t,
		`INSERT INTO memberships (id, organization_id, user_id, role) VALUES ($1, $2, $3, $4)`,
		ids.MustNew("mem"), org.organization.ID, viewerID, domain.RoleViewer)
	err = env.projects.Create(ctx, domain.Project{
		ID:             ids.MustNew("prj"),
		OrganizationID: org.organization.ID,
		Name:           "viewer-attempt",
	}, viewerID)
	if !errors.Is(err, domain.ErrPermissionDenied) {
		t.Fatalf("viewer err = %v, want ErrPermissionDenied", err)
	}
}

func TestProjectDuplicateNameAndReuseAfterSoftDelete(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	org := env.seedOrganization(t, ctx)

	name := "duplicate-" + ids.MustNew("prj")
	created := env.seedProject(t, ctx, org, name)

	err := env.projects.Create(ctx, domain.Project{
		ID:             ids.MustNew("prj"),
		OrganizationID: org.organization.ID,
		Name:           name,
	}, org.ownerID)
	var conflict *domain.ConflictError
	if !errors.As(err, &conflict) || conflict.Code != domain.ConflictDuplicateName {
		t.Fatalf("err = %v, want duplicate_name conflict", err)
	}

	env.softDeleteProject(ctx, t, created.ID)
	reused := env.seedProject(t, ctx, org, name)
	if reused.ID == created.ID {
		t.Fatal("expected a new project row after soft delete")
	}
}

func TestProjectUpdateOptimisticConcurrency(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	org := env.seedOrganization(t, ctx)
	project := env.seedProject(t, ctx, org, "concurrency")

	// A stale version loses.
	_, err := env.projects.UpdateForOrg(ctx, org.organization.ID, project.ID, project.Version+7,
		domain.ProjectUpdate{Description: strPtr("should not apply")}, org.ownerID)
	var conflict *domain.ConflictError
	if !errors.As(err, &conflict) || conflict.Code != domain.ConflictVersionMismatch {
		t.Fatalf("err = %v, want version_mismatch", err)
	}
	if conflict.CurrentVersion != project.Version {
		t.Fatalf("CurrentVersion = %d, want %d", conflict.CurrentVersion, project.Version)
	}

	// The correct version wins and increments exactly once.
	updated, err := env.projects.UpdateForOrg(ctx, org.organization.ID, project.ID, project.Version,
		domain.ProjectUpdate{
			Description: strPtr("updated description"),
			Settings:    map[string]any{domain.SettingDefaultBranch: "main", domain.SettingExternalPRAllowed: true},
		}, org.ownerID)
	if err != nil {
		t.Fatalf("UpdateForOrg: %v", err)
	}
	if updated.Version != project.Version+1 {
		t.Fatalf("version = %d, want %d", updated.Version, project.Version+1)
	}
	if updated.Description != "updated description" {
		t.Fatalf("description = %q", updated.Description)
	}
	if updated.Settings[domain.SettingExternalPRAllowed] != true {
		t.Fatalf("settings = %v", updated.Settings)
	}

	revisions := env.settingsRevisions(ctx, t, project.ID)
	if len(revisions) != 1 {
		t.Fatalf("settings revisions = %d, want 1", len(revisions))
	}
	if revisions[0].version != updated.Version {
		t.Fatalf("revision version = %d, want %d", revisions[0].version, updated.Version)
	}
	if revisions[0].changedBy == nil || *revisions[0].changedBy != org.ownerID {
		t.Fatalf("revision changed_by = %v, want %s", revisions[0].changedBy, org.ownerID)
	}

	// A name update that collides with a live project surfaces a conflict.
	other := env.seedProject(t, ctx, org, "collision")
	_, err = env.projects.UpdateForOrg(ctx, org.organization.ID, project.ID, updated.Version,
		domain.ProjectUpdate{Name: strPtr(other.Name)}, org.ownerID)
	if !errors.As(err, &conflict) || conflict.Code != domain.ConflictDuplicateName {
		t.Fatalf("rename err = %v, want duplicate_name", err)
	}
}

func TestProjectUpdateMissingProjectIsNotFound(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	org := env.seedOrganization(t, ctx)

	_, err := env.projects.UpdateForOrg(ctx, org.organization.ID, ids.MustNew("prj"), 1,
		domain.ProjectUpdate{Description: strPtr("nope")}, org.ownerID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAuditAppendAndPaginate(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	org := env.seedOrganization(t, ctx)
	project := env.seedProject(t, ctx, org, "audited")

	const total = 12
	now := time.Now().UTC()
	for i := 0; i < total; i++ {
		event := domain.AuditEvent{
			ID:             ids.MustNew("aud"),
			OrganizationID: org.organization.ID,
			ProjectID:      project.ID,
			ActorID:        org.ownerID,
			Action:         fmt.Sprintf("test.action_%02d", i),
			EntityType:     "project",
			EntityID:       project.ID,
			RequestID:      ids.MustNew("req"),
			Payload:        map[string]any{"index": i},
		}
		if err := env.audit.Append(ctx, []domain.AuditEvent{event}); err != nil {
			t.Fatalf("Append %d: %v", i, err)
		}
	}
	if time.Since(now) > 5*time.Minute {
		t.Fatal("clock skew in test environment")
	}

	seen := map[int64]bool{}
	cursor := ""
	for {
		page, err := env.audit.ListForProject(ctx, org.organization.ID, project.ID, "", 5, cursor)
		if err != nil {
			t.Fatalf("ListForProject: %v", err)
		}
		for _, event := range page.Items {
			if event.ProjectID != project.ID {
				t.Fatalf("foreign project event %s", event.ID)
			}
			if seen[event.Sequence] {
				t.Fatalf("sequence %d returned twice", event.Sequence)
			}
			seen[event.Sequence] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) < total {
		t.Fatalf("saw %d audit events, want at least %d", len(seen), total)
	}

	// Action filter narrows to an exact match.
	filtered, err := env.audit.ListForProject(ctx, org.organization.ID, project.ID, "test.action_00", 50, "")
	if err != nil {
		t.Fatalf("filtered list: %v", err)
	}
	if len(filtered.Items) != 1 {
		t.Fatalf("filtered events = %d, want 1", len(filtered.Items))
	}
}

func TestAuditAppendRejectsEmptyBatch(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	if err := env.audit.Append(ctx, nil); !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("err = %v, want ErrValidation", err)
	}
}

func TestIdempotencyRoundTrip(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	org := env.seedOrganization(t, ctx)

	key := ids.MustNew("idk")
	_, _, found, err := env.idempotency.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found {
		t.Fatal("key should not exist yet")
	}

	body := map[string]any{"id": ids.MustNew("prj"), "state": "queued"}
	if err := env.idempotency.Store(ctx, key, org.organization.ID, "POST /api/v1/projects", "hash-1", 201, body, time.Hour); err != nil {
		t.Fatalf("Store: %v", err)
	}
	status, storedBody, found, err := env.idempotency.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found || status != 201 {
		t.Fatalf("found = %v status = %d", found, status)
	}
	if storedBody["id"] != body["id"] {
		t.Fatalf("body = %v, want %v", storedBody, body)
	}

	// A second store with the same key must not fail (callers treat the first
	// stored response as authoritative).
	if err := env.idempotency.Store(ctx, key, org.organization.ID, "POST /api/v1/projects", "hash-1", 201, body, time.Hour); err != nil {
		t.Fatalf("duplicate Store: %v", err)
	}
}

func TestUsersUpsertIsIdempotent(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	email := "upsert-" + ids.MustNew("usr") + "@example.test"

	first, err := env.users.UpsertByEmail(ctx, email, "First")
	if err != nil {
		t.Fatalf("UpsertByEmail: %v", err)
	}
	second, err := env.users.UpsertByEmail(ctx, strings.ToUpper(email), "Second")
	if err != nil {
		t.Fatalf("UpsertByEmail (upper): %v", err)
	}
	if first != second {
		t.Fatalf("user id changed across upserts: %s vs %s", first, second)
	}
	user, err := env.users.Get(ctx, second)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if user.DisplayName != "Second" {
		t.Fatalf("display name = %q, want Second", user.DisplayName)
	}
}

func TestProjectListFiltersByName(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	org := env.seedOrganization(t, ctx)

	target := "findme-" + ids.MustNew("prj")
	env.seedProject(t, ctx, org, target)
	env.seedProject(t, ctx, org, "other-"+ids.MustNew("prj"))

	page, err := env.projects.ListForOrg(ctx, org.organization.ID, "findme", 50, "")
	if err != nil {
		t.Fatalf("ListForOrg: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Name != target {
		t.Fatalf("filtered items = %+v", page.Items)
	}

	// Wildcards in the filter are literal, not patterns.
	wildcardPage, err := env.projects.ListForOrg(ctx, org.organization.ID, "%", 50, "")
	if err != nil {
		t.Fatalf("ListForOrg (wildcard): %v", err)
	}
	if len(wildcardPage.Items) != 0 {
		t.Fatalf("wildcard filter matched %d items", len(wildcardPage.Items))
	}
}

func strPtr(value string) *string { return &value }

package store

// Integration tests run against a real PostgreSQL instance. They are skipped
// unless RS_TEST_DATABASE_URL points at a disposable database, and are wired
// into CI through the postgres service container. Every test mints its own
// identifiers and emails so a database reused across runs is never polluted:
// nothing here truncates or resets a shared table.

import (
	"context"
	"encoding/base64"
	"os"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/YASSERRMD/RepoSurgeon/internal/secrets"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testEnv bundles every M01 repository over one pool, opened through the same
// configuration the API uses.
type testEnv struct {
	pool          *pgxpool.Pool
	organizations *Organizations
	projects      *Projects
	audit         *Audit
	idempotency   *Idempotency
	users         *Users
}

// openStorePool connects a pool to the test database with the same options the
// API uses. It is shared by every integration test in this package.
func openStorePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("RS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RS_TEST_DATABASE_URL not set; skipping PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := Open(ctx, dsn, DefaultOptions())
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// newTestEncryptor builds the encryptor used by credential tests.
func newTestEncryptor(t *testing.T) *secrets.Encryptor {
	t.Helper()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = 0x5a
	}
	encryptor, err := secrets.New(base64.StdEncoding.EncodeToString(raw), 1)
	if err != nil {
		t.Fatalf("new encryptor: %v", err)
	}
	return encryptor
}

func fingerprintOf(value string) string { return secrets.Fingerprint(value) }

func openTestEnv(t *testing.T) testEnv {
	t.Helper()
	dsn := os.Getenv("RS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RS_TEST_DATABASE_URL not set; skipping PostgreSQL integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := Open(ctx, dsn, DefaultOptions())
	if err != nil {
		t.Fatalf("open test pool: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return testEnv{
		pool:          pool,
		organizations: NewOrganizations(pool),
		projects:      NewProjects(pool),
		audit:         NewAudit(pool),
		idempotency:   NewIdempotency(pool),
		users:         NewUsers(pool),
	}
}

// orgFixture is a disposable organization and its owner, both minted by the
// repositories under test.
type orgFixture struct {
	organization domain.Organization
	ownerID      string
	ownerEmail   string
}

// seedOrganization creates a fresh user and an organization they own.
func (env testEnv) seedOrganization(t *testing.T, ctx context.Context) orgFixture {
	t.Helper()
	userID := ids.MustNew("usr")
	email := "user-" + userID + "@example.test"
	ownerID, err := env.users.UpsertByEmail(ctx, email, "Owner "+userID)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}

	orgID := ids.MustNew("org")
	org := domain.Organization{
		ID:   orgID,
		Slug: ids.MustNew("org"),
		Name: "Organization " + orgID,
	}
	if err := env.organizations.Create(ctx, org, ownerID, domain.RoleOwner); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	return orgFixture{organization: org, ownerID: ownerID, ownerEmail: email}
}

// seedProject creates a project in the organization and reads it back, so
// callers see exactly what the database stored.
func (env testEnv) seedProject(t *testing.T, ctx context.Context, fixture orgFixture, name string) domain.Project {
	t.Helper()
	project := domain.Project{
		ID:             ids.MustNew("prj"),
		OrganizationID: fixture.organization.ID,
		Name:           name,
		Description:    "seeded project " + name,
	}
	if err := env.projects.Create(ctx, project, fixture.ownerID); err != nil {
		t.Fatalf("seed project %q: %v", name, err)
	}
	stored, err := env.projects.GetForOrg(ctx, fixture.organization.ID, project.ID)
	if err != nil {
		t.Fatalf("read back project %q: %v", name, err)
	}
	return stored
}

// exec runs a test-only statement against the database.
func (env testEnv) exec(ctx context.Context, t *testing.T, sql string, args ...any) {
	t.Helper()
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	if _, err := env.pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// countInt runs a scalar count query used to assert row-level effects such as
// settings revisions.
func (env testEnv) countInt(ctx context.Context, t *testing.T, sql string, args ...any) int64 {
	t.Helper()
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	var count int64
	if err := env.pool.QueryRow(ctx, sql, args...).Scan(&count); err != nil {
		t.Fatalf("count %q: %v", sql, err)
	}
	return count
}

// settingsRevision is one row of project_settings_revisions as stored.
type settingsRevision struct {
	version   int64
	settings  map[string]any
	changedBy *string
}

// settingsRevisions returns a project's settings revisions oldest first.
func (env testEnv) settingsRevisions(ctx context.Context, t *testing.T, projectID string) []settingsRevision {
	t.Helper()
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	rows, err := env.pool.Query(ctx,
		`SELECT version, settings, changed_by FROM project_settings_revisions
		  WHERE project_id = $1 ORDER BY version ASC`, projectID)
	if err != nil {
		t.Fatalf("read settings revisions: %v", err)
	}
	revisions, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (settingsRevision, error) {
		var revision settingsRevision
		err := row.Scan(&revision.version, &revision.settings, &revision.changedBy)
		return revision, err
	})
	if err != nil {
		t.Fatalf("scan settings revisions: %v", err)
	}
	return revisions
}

// softDeleteProject hides a project without going through a repository, which
// is how a delete path would leave the row.
func (env testEnv) softDeleteProject(ctx context.Context, t *testing.T, projectID string) {
	t.Helper()
	env.exec(ctx, t, `UPDATE projects SET deleted_at = now() WHERE id = $1`, projectID)
}

func TestMain(m *testing.M) {
	// Integration tests need a disposable PostgreSQL database; unit tests run
	// without one.
	os.Exit(m.Run())
}

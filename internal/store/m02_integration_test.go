package store

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/YASSERRMD/RepoSurgeon/internal/auth"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
)

// newM02Env wires the M02 repositories over one pool.
type m02Env struct {
	pool        *pgxpool.Pool
	users       *Users
	orgs        *Organizations
	members     *Members
	sessions    *Sessions
	identities  *Identities
	credentials *Credentials
}

func openM02Env(t *testing.T) m02Env {
	t.Helper()
	pool := openStorePool(t)
	env := m02Env{
		pool:        pool,
		users:       NewUsers(pool),
		orgs:        NewOrganizations(pool),
		members:     NewMembers(pool),
		sessions:    NewSessions(pool),
		identities:  NewIdentities(pool),
		credentials: NewCredentials(pool),
	}
	return env
}

// createUser makes a user with a local identity and returns their id.
func (env m02Env) createUser(t *testing.T, ctx context.Context, email string) string {
	t.Helper()
	userID, err := env.users.UpsertByEmail(ctx, email, email)
	if err != nil {
		t.Fatalf("upsert user: %v", err)
	}
	hash, err := auth.HashPassword("test-password-1234")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if err := env.identities.CreateLocal(ctx, userID, email, hash); err != nil {
		t.Fatalf("create identity: %v", err)
	}
	return userID
}

// createOrg makes an organization owned by ownerID.
func (env m02Env) createOrg(t *testing.T, ctx context.Context, ownerID, name string) domain.Organization {
	t.Helper()
	org := domain.Organization{
		ID:        ids.MustNew("org"),
		Slug:      ids.MustNew("org"),
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}
	org.UpdatedAt = org.CreatedAt
	if err := env.orgs.Create(ctx, org, ownerID, domain.RoleOwner); err != nil {
		t.Fatalf("create org: %v", err)
	}
	return org
}

func TestLocalIdentityLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env := openM02Env(t)
	ctx := context.Background()
	email := "login-" + ids.MustNew("usr") + "@example.test"
	userID := env.createUser(t, ctx, email)

	identity, err := env.identities.GetLocalByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetLocalByEmail: %v", err)
	}
	if identity.UserID != userID {
		t.Fatalf("identity user = %s, want %s", identity.UserID, userID)
	}
	ok, err := auth.VerifyPassword(identity.PasswordHash, "test-password-1234")
	if err != nil || !ok {
		t.Fatalf("password verification failed: ok=%v err=%v", ok, err)
	}
	ok, err = auth.VerifyPassword(identity.PasswordHash, "wrong-password-1234")
	if err != nil || ok {
		t.Fatalf("wrong password accepted: ok=%v err=%v", ok, err)
	}

	// Case-insensitive subject lookup.
	if _, err := env.identities.GetLocalByEmail(ctx, upperEmail(email)); err != nil {
		t.Fatalf("uppercase lookup failed: %v", err)
	}

	// Unknown email is not found.
	if _, err := env.identities.GetLocalByEmail(ctx, "nobody@example.test"); !domain.IsNotFound(err) {
		t.Fatalf("unknown email err = %v, want not found", err)
	}

	// Bootstrap gate: once an identity exists, bootstrap must refuse.
	exists, err := env.identities.HasLocalIdentity(ctx)
	if err != nil || !exists {
		t.Fatalf("HasLocalIdentity = %v err = %v", exists, err)
	}
}

func TestSessionLifecycleAndRotation(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env := openM02Env(t)
	ctx := context.Background()
	userID := env.createUser(t, ctx, "session-"+ids.MustNew("usr")+"@example.test")

	token, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	_, csrfHash, err := auth.NewCSRFToken()
	if err != nil {
		t.Fatalf("NewCSRFToken: %v", err)
	}
	if _, err := env.sessions.Create(ctx, userID, hash, csrfHash, "iphash", "agent", time.Hour); err != nil {
		t.Fatalf("Create: %v", err)
	}

	session, err := env.sessions.Get(ctx, hash)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if session.UserID != userID || !auth.ConstantTimeEqual(session.CSRFHash, csrfHash) {
		t.Fatalf("session = %+v", session)
	}

	// Rotation revokes the old token and links the new one. The client-side
	// token string is not needed because sessions are addressed by hash.
	_, newHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	_, newCSRF, err := auth.NewCSRFToken()
	if err != nil {
		t.Fatalf("NewCSRFToken: %v", err)
	}
	if _, err := env.sessions.Rotate(ctx, hash, newHash, newCSRF, "iphash", "agent", time.Hour); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	if _, err := env.sessions.Get(ctx, hash); !domain.IsNotFound(err) {
		t.Fatal("rotated session must no longer be usable")
	}
	if _, err := env.sessions.Get(ctx, newHash); err != nil {
		t.Fatalf("rotated session must be active: %v", err)
	}

	// Replay of the revoked token is rejected.
	_, replayHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken (replay): %v", err)
	}
	if _, err := env.sessions.Rotate(ctx, hash, replayHash, "csrf", "iphash", "agent", time.Hour); !domain.IsNotFound(err) {
		t.Fatal("replayed token must be rejected")
	}
	_ = token

	// Logout revokes.
	if err := env.sessions.Revoke(ctx, newHash, "logout"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := env.sessions.Get(ctx, newHash); !domain.IsNotFound(err) {
		t.Fatal("revoked session must not resolve")
	}

	// Revoke-all for a user revokes every remaining session.
	_, secondHash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if _, err := env.sessions.Create(ctx, userID, secondHash, "csrf", "ip", "agent", time.Hour); err != nil {
		t.Fatalf("Create second: %v", err)
	}
	affected, err := env.sessions.RevokeAllForUser(ctx, userID, "admin")
	if err != nil {
		t.Fatalf("RevokeAllForUser: %v", err)
	}
	if affected < 1 {
		t.Fatalf("RevokeAllForUser affected %d rows", affected)
	}
}

func TestSessionExpiryIsEnforced(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env := openM02Env(t)
	ctx := context.Background()
	userID := env.createUser(t, ctx, "expiry-"+ids.MustNew("usr")+"@example.test")

	// Create normalises a non-positive TTL to the default, so the expired row
	// is written directly to exercise the Get path faithfully.
	expiredID := auth.HashToken([]byte("expired-" + ids.MustNew("ses")))
	if _, err := env.pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, expires_at, csrf_hash)
		VALUES ($1, $2, now() - interval '1 minute', 'csrf')`, expiredID, userID); err != nil {
		t.Fatalf("insert expired session: %v", err)
	}
	if _, err := env.sessions.Get(ctx, expiredID); !domain.IsNotFound(err) {
		t.Fatal("expired session must not resolve")
	}
}

func TestFailedLoginAttemptsAreTracked(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env := openM02Env(t)
	ctx := context.Background()
	email := "attempts-" + ids.MustNew("usr") + "@example.test"

	for i := 0; i < 3; i++ {
		if err := env.sessions.RecordAttempt(ctx, email, false); err != nil {
			t.Fatalf("RecordAttempt: %v", err)
		}
	}
	count, err := env.sessions.CountRecentFailedAttempts(ctx, email, 15*time.Minute)
	if err != nil {
		t.Fatalf("CountRecentFailedAttempts: %v", err)
	}
	if count != 3 {
		t.Fatalf("count = %d, want 3", count)
	}
	// A different window excludes them.
	count, err = env.sessions.CountRecentFailedAttempts(ctx, email, -time.Minute)
	if err != nil {
		t.Fatalf("CountRecentFailedAttempts (old window): %v", err)
	}
	if count != 0 {
		t.Fatalf("count with past window = %d, want 0", count)
	}
}

func TestRolePermissionMatrix(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env := openM02Env(t)
	ctx := context.Background()

	ownerEmail := "owner-" + ids.MustNew("usr") + "@example.test"
	adminEmail := "admin-" + ids.MustNew("usr") + "@example.test"
	maintainerEmail := "maint-" + ids.MustNew("usr") + "@example.test"
	reviewerEmail := "rev-" + ids.MustNew("usr") + "@example.test"
	memberEmail := "mem-" + ids.MustNew("usr") + "@example.test"
	viewerEmail := "view-" + ids.MustNew("usr") + "@example.test"
	outsiderEmail := "out-" + ids.MustNew("usr") + "@example.test"

	owner := env.createUser(t, ctx, ownerEmail)
	admin := env.createUser(t, ctx, adminEmail)
	maintainer := env.createUser(t, ctx, maintainerEmail)
	reviewer := env.createUser(t, ctx, reviewerEmail)
	member := env.createUser(t, ctx, memberEmail)
	viewer := env.createUser(t, ctx, viewerEmail)
	outsider := env.createUser(t, ctx, outsiderEmail)

	org := env.createOrg(t, ctx, owner, "matrix")
	for _, tc := range []struct {
		user string
		role string
	}{
		{adminEmail, domain.RoleAdmin},
		{maintainerEmail, "maintainer"},
		{reviewerEmail, "reviewer"},
		{memberEmail, domain.RoleMember},
		{viewerEmail, domain.RoleViewer},
	} {
		if _, err := env.members.Invite(ctx, org.ID, tc.user, tc.role, tc.user); err != nil {
			t.Fatalf("invite %s: %v", tc.role, err)
		}
	}

	expect := map[string][]string{
		owner:      {"project.read", "project.write", "run.execute", "credential.admin", "member.admin", "approval.decide", "merge.execute"},
		admin:      {"project.read", "project.write", "run.execute", "credential.admin", "member.admin", "approval.decide"},
		maintainer: {"project.read", "project.write", "run.execute", "approval.decide"},
		reviewer:   {"project.read", "approval.decide"},
		member:     {"project.read", "project.write"},
		viewer:     {"project.read"},
	}
	for user, want := range expect {
		got, err := env.members.PermissionsFor(ctx, org.ID, user)
		if err != nil {
			t.Fatalf("PermissionsFor: %v", err)
		}
		if !sameStrings(got, want) {
			t.Fatalf("permissions for role = %v, want %v", got, want)
		}
	}

	// Outsiders have no permissions at all.
	got, err := env.members.PermissionsFor(ctx, org.ID, outsider)
	if err != nil {
		t.Fatalf("PermissionsFor outsider: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("outsider permissions = %v, want none", got)
	}
	if _, err := env.members.RoleFor(ctx, org.ID, outsider); !domain.IsNotFound(err) {
		t.Fatalf("outsider role err = %v, want not found", err)
	}
}

func TestMemberRoleLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env := openM02Env(t)
	ctx := context.Background()
	owner := env.createUser(t, ctx, "owner2-"+ids.MustNew("usr")+"@example.test")
	org := env.createOrg(t, ctx, owner, "roles")

	teammate := env.createUser(t, ctx, "team-"+ids.MustNew("usr")+"@example.test")
	invited, err := env.members.Invite(ctx, org.ID, teammate, domain.RoleViewer, teammate)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}

	updated, err := env.members.UpdateRole(ctx, org.ID, invited.ID, "maintainer")
	if err != nil {
		t.Fatalf("UpdateRole: %v", err)
	}
	if updated.Role != "maintainer" {
		t.Fatalf("role = %q, want maintainer", updated.Role)
	}

	// Owners are counted so the last owner can be protected.
	count, err := env.members.CountOwners(ctx, org.ID)
	if err != nil {
		t.Fatalf("CountOwners: %v", err)
	}
	if count != 1 {
		t.Fatalf("owners = %d, want 1", count)
	}

	if err := env.members.Remove(ctx, org.ID, invited.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := env.members.Get(ctx, org.ID, invited.ID); !domain.IsNotFound(err) {
		t.Fatal("removed membership must be gone")
	}
}

func TestCredentialEncryptionRoundTripThroughStore(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env := openM02Env(t)
	ctx := context.Background()
	owner := env.createUser(t, ctx, "cred-"+ids.MustNew("usr")+"@example.test")
	org := env.createOrg(t, ctx, owner, "credentials")

	encryptor := newTestEncryptor(t)
	value := "sf-live-" + ids.MustNew("key")
	ciphertext, nonce, err := encryptor.EncryptSecret([]byte(value))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	credential := Credential{
		ID:             ids.MustNew("crd"),
		OrganizationID: org.ID,
		Kind:           "stepfun",
		Label:          "primary",
		Status:         "active",
		Ciphertext:     ciphertext,
		Nonce:          nonce,
		KeyVersion:     encryptor.KeyVersion(),
		Fingerprint:    fingerprintOf(value),
		CreatedBy:      owner,
		CreatedAt:      time.Now().UTC(),
	}
	credential.UpdatedAt = credential.CreatedAt
	if err := env.credentials.Create(ctx, credential); err != nil {
		t.Fatalf("Create: %v", err)
	}

	stored, err := env.credentials.Get(ctx, org.ID, credential.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	recovered, err := encryptor.DecryptSecret(stored.Ciphertext, stored.Nonce)
	if err != nil {
		t.Fatalf("DecryptSecret: %v", err)
	}
	if string(recovered) != value {
		t.Fatal("decrypted value does not match")
	}

	// Cross-tenant read is impossible.
	otherOwner := env.createUser(t, ctx, "other-"+ids.MustNew("usr")+"@example.test")
	otherOrg := env.createOrg(t, ctx, otherOwner, "other-credentials")
	if _, err := env.credentials.Get(ctx, otherOrg.ID, credential.ID); !domain.IsNotFound(err) {
		t.Fatal("cross-tenant credential read must fail")
	}

	// The list never carries the value.
	page, err := env.credentials.List(ctx, org.ID, 10, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("items = %d, want 1", len(page.Items))
	}

	// Rotation replaces the value and bumps the key version.
	rotatedValue := "sf-live-" + ids.MustNew("key2")
	rotated, rotatedNonce, err := encryptor.EncryptSecret([]byte(rotatedValue))
	if err != nil {
		t.Fatalf("EncryptSecret (rotated): %v", err)
	}
	newVersion := encryptor.KeyVersion() + 1
	if err := env.credentials.Rotate(ctx, org.ID, credential.ID, rotated, rotatedNonce, newVersion, fingerprintOf(rotatedValue)); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	after, err := env.credentials.Get(ctx, org.ID, credential.ID)
	if err != nil {
		t.Fatalf("Get after rotate: %v", err)
	}
	if after.KeyVersion != newVersion {
		t.Fatalf("key version = %d, want %d", after.KeyVersion, newVersion)
	}
	recoveredRotated, err := encryptor.DecryptSecret(after.Ciphertext, after.Nonce)
	if err != nil {
		t.Fatalf("DecryptSecret after rotate: %v", err)
	}
	if string(recoveredRotated) != rotatedValue {
		t.Fatal("rotated value does not decrypt")
	}

	// Revocation stops rotation but keeps metadata.
	if err := env.credentials.Revoke(ctx, org.ID, credential.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if err := env.credentials.Rotate(ctx, org.ID, credential.ID, rotated, rotatedNonce, newVersion, fingerprintOf(rotatedValue)); err == nil {
		t.Fatal("rotating a revoked credential must fail")
	}
	revoked, err := env.credentials.Get(ctx, org.ID, credential.ID)
	if err != nil {
		t.Fatalf("Get revoked: %v", err)
	}
	if revoked.Status != "revoked" {
		t.Fatalf("status = %q, want revoked", revoked.Status)
	}
	if revoked.Label != "primary" {
		t.Fatalf("label = %q, want preserved for audit", revoked.Label)
	}
}

func TestCredentialDuplicateFingerprintIsRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	env := openM02Env(t)
	ctx := context.Background()
	owner := env.createUser(t, ctx, "dup-"+ids.MustNew("usr")+"@example.test")
	org := env.createOrg(t, ctx, owner, "duplicates")
	encryptor := newTestEncryptor(t)

	value := "sf-live-duplicate-value"
	ciphertext, nonce, err := encryptor.EncryptSecret([]byte(value))
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	credential := Credential{
		ID: ids.MustNew("crd"), OrganizationID: org.ID, Kind: "stepfun", Label: "first",
		Status: "active", Ciphertext: ciphertext, Nonce: nonce,
		KeyVersion: encryptor.KeyVersion(), Fingerprint: fingerprintOf(value),
		CreatedBy: owner, CreatedAt: time.Now().UTC(),
	}
	credential.UpdatedAt = credential.CreatedAt
	if err := env.credentials.Create(ctx, credential); err != nil {
		t.Fatalf("Create: %v", err)
	}

	duplicate := credential
	duplicate.ID = ids.MustNew("crd")
	duplicate.Label = "second"
	if err := env.credentials.Create(ctx, duplicate); err == nil {
		t.Fatal("duplicate fingerprint must be rejected")
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := map[string]int{}
	for _, v := range got {
		seen[v]++
	}
	for _, v := range want {
		seen[v]--
		if seen[v] < 0 {
			return false
		}
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}

func upperEmail(email string) string {
	out := []byte(email)
	for i := range out {
		if out[i] >= 'a' && out[i] <= 'z' {
			out[i] -= 32
		}
	}
	return string(out)
}

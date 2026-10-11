package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Session is an authenticated server-side session.
type Session struct {
	ID        string
	UserID    string
	ExpiresAt time.Time
	RevokedAt *time.Time
	CSRFHash  string
}

// Sessions manages server-side session lifecycle: create, rotate, verify and
// revoke. Tokens are stored only as hashes.
type Sessions struct {
	pool *pgxpool.Pool
}

// SessionTTL is the lifetime of a session before rotation is required.
const SessionTTL = 12 * time.Hour

// Create inserts a session for userID and returns the stored hash.
func (r *Sessions) Create(ctx context.Context, userID, tokenHash, csrfHash, ipHash, userAgent string, ttl time.Duration) (Session, error) {
	if ttl <= 0 {
		ttl = SessionTTL
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	session := Session{
		ID:        tokenHash,
		UserID:    userID,
		CSRFHash:  csrfHash,
		ExpiresAt: time.Now().UTC().Add(ttl),
	}
	_, err := r.pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, expires_at, ip_hash, user_agent, csrf_hash)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		session.ID, session.UserID, session.ExpiresAt, ipHash, userAgent, csrfHash)
	if err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	return session, nil
}

// Get returns a live (unexpired, unrevoked) session by token hash.
func (r *Sessions) Get(ctx context.Context, tokenHash string) (Session, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	var session Session
	var revokedAt *time.Time
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, expires_at, revoked_at, csrf_hash
		FROM sessions WHERE id = $1`, tokenHash).
		Scan(&session.ID, &session.UserID, &session.ExpiresAt, &revokedAt, &session.CSRFHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, fmt.Errorf("session: %w", domain.ErrNotFound)
		}
		return Session{}, fmt.Errorf("get session: %w", err)
	}
	session.RevokedAt = revokedAt
	if revokedAt != nil || time.Now().UTC().After(session.ExpiresAt) {
		return Session{}, fmt.Errorf("session: %w", domain.ErrNotFound)
	}
	return session, nil
}

// Touch updates last_seen_at for an active session.
func (r *Sessions) Touch(ctx context.Context, tokenHash string, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Minute
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	_, err := r.pool.Exec(ctx, `
		UPDATE sessions SET last_seen_at = now()
		WHERE id = $1 AND revoked_at IS NULL AND expires_at > now()
		  AND last_seen_at < now() - make_interval(secs => $2)`,
		tokenHash, interval.Seconds())
	if err != nil {
		return fmt.Errorf("touch session: %w", err)
	}
	return nil
}

// Rotate revokes tokenHash and creates a replacement, linking the two so a
// replayed old token is detectable.
func (r *Sessions) Rotate(ctx context.Context, oldHash, newHash, newCSRFHash, ipHash, userAgent string, ttl time.Duration) (Session, error) {
	if ttl <= 0 {
		ttl = SessionTTL
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()

	var userID string
	err := r.pool.QueryRow(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = 'rotated'
		WHERE id = $1 AND revoked_at IS NULL AND expires_at > now()
		RETURNING user_id`, oldHash).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Session{}, fmt.Errorf("session: %w", domain.ErrNotFound)
		}
		return Session{}, fmt.Errorf("rotate session: %w", err)
	}

	session := Session{ID: newHash, UserID: userID, CSRFHash: newCSRFHash, ExpiresAt: time.Now().UTC().Add(ttl)}
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO sessions (id, user_id, expires_at, ip_hash, user_agent, csrf_hash, rotated_from)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		newHash, userID, session.ExpiresAt, ipHash, userAgent, newCSRFHash, oldHash); err != nil {
		return Session{}, fmt.Errorf("create rotated session: %w", err)
	}
	return session, nil
}

// Revoke marks a session revoked.
func (r *Sessions) Revoke(ctx context.Context, tokenHash, reason string) error {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	_, err := r.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = $2
		WHERE id = $1`, tokenHash, reason)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// RevokeAllForUser revokes every live session of a user.
func (r *Sessions) RevokeAllForUser(ctx context.Context, userID, reason string) (int64, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	tag, err := r.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = now(), revoked_reason = $2
		WHERE user_id = $1 AND revoked_at IS NULL`, userID, reason)
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// CountRecentFailedAttempts counts failed logins for an email since.
func (r *Sessions) CountRecentFailedAttempts(ctx context.Context, email string, since time.Duration) (int, error) {
	// Login attempts are recorded in the same database; a nil pool in tests is
	// treated as "no attempts recorded".
	if r.pool == nil {
		return 0, nil
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	var count int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM login_attempts
		WHERE email_normalized = lower($1) AND succeeded = false
		  AND attempted_at > now() - make_interval(secs => $2)`,
		email, since.Seconds()).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count failed attempts: %w", err)
	}
	return count, nil
}

// RecordAttempt appends a login attempt.
func (r *Sessions) RecordAttempt(ctx context.Context, email string, succeeded bool) error {
	if r.pool == nil {
		return nil
	}
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	if _, err := r.pool.Exec(ctx, `
		INSERT INTO login_attempts (email_normalized, succeeded) VALUES (lower($1), $2)`,
		email, succeeded); err != nil {
		return fmt.Errorf("record attempt: %w", err)
	}
	return nil
}

// ---- identities ------------------------------------------------------------

// Identity is a local or future OIDC identity.
type Identity struct {
	ID           string
	UserID       string
	Kind         string
	Subject      string
	PasswordHash string
}

// Identities resolves identities by (kind, subject).
type Identities struct {
	pool *pgxpool.Pool
}

// GetLocalByEmail returns the local identity for an email address.
func (r *Identities) GetLocalByEmail(ctx context.Context, email string) (Identity, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	var identity Identity
	err := r.pool.QueryRow(ctx, `
		SELECT id, user_id, kind, subject, coalesce(password_hash, '')
		FROM auth_identities WHERE kind = 'local' AND subject = lower($1)`, email).
		Scan(&identity.ID, &identity.UserID, &identity.Kind, &identity.Subject, &identity.PasswordHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Identity{}, fmt.Errorf("identity: %w", domain.ErrNotFound)
		}
		return Identity{}, fmt.Errorf("get identity: %w", err)
	}
	return identity, nil
}

// CreateLocal stores a local identity for an existing user.
func (r *Identities) CreateLocal(ctx context.Context, userID, email, passwordHash string) error {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	_, err := r.pool.Exec(ctx, `
		INSERT INTO auth_identities (id, user_id, kind, subject, password_hash)
		VALUES (gen_random_uuid()::text, $1, 'local', lower($2), $3)`,
		userID, email, passwordHash)
	if err != nil {
		return fmt.Errorf("create identity: %w", err)
	}
	return nil
}

// HasLocalIdentity reports whether any local identity exists. It gates the
// development bootstrap endpoint.
func (r *Identities) HasLocalIdentity(ctx context.Context) (bool, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	var exists bool
	if err := r.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM auth_identities WHERE kind = 'local')`).Scan(&exists); err != nil {
		return false, fmt.Errorf("check identities: %w", err)
	}
	return exists, nil
}

// ---- credentials -----------------------------------------------------------

// Credential is the stored, encrypted provider credential.
type Credential struct {
	ID             string
	OrganizationID string
	Kind           string
	Label          string
	Status         string
	Ciphertext     []byte
	Nonce          []byte
	KeyVersion     int
	Fingerprint    string
	CreatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	LastVerifiedAt *time.Time
}

// Credentials stores and reads encrypted credentials. Values never leave this
// repository in plaintext form through any public API.
type Credentials struct {
	pool *pgxpool.Pool
}

const credentialColumns = `id, organization_id, kind, label, status, ciphertext, nonce,
	key_version, fingerprint, coalesce(created_by, ''), created_at, updated_at, last_verified_at`

type scannable interface{ Scan(...any) error }

func collectCredentials(rows pgx.Rows) ([]Credential, error) {
	var out []Credential
	for rows.Next() {
		credential, err := scanCredential(rows)
		if err != nil {
			return nil, fmt.Errorf("scan credential: %w", err)
		}
		out = append(out, credential)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate credentials: %w", err)
	}
	return out, nil
}

func scanCredential(row scannable) (Credential, error) {
	var credential Credential
	err := row.Scan(
		&credential.ID, &credential.OrganizationID, &credential.Kind, &credential.Label,
		&credential.Status, &credential.Ciphertext, &credential.Nonce, &credential.KeyVersion,
		&credential.Fingerprint, &credential.CreatedBy, &credential.CreatedAt, &credential.UpdatedAt,
		&credential.LastVerifiedAt)
	return credential, err
}

// Create inserts a credential inside a transaction.
func (r *Credentials) Create(ctx context.Context, credential Credential) error {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	_, err := r.pool.Exec(ctx, `
		INSERT INTO credential_metadata
			(id, organization_id, kind, label, status, ciphertext, nonce, key_version,
			 fingerprint, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		credential.ID, credential.OrganizationID, credential.Kind, credential.Label, credential.Status,
		credential.Ciphertext, credential.Nonce, credential.KeyVersion, credential.Fingerprint,
		credential.CreatedBy)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("credential %s/%s: %w", credential.Kind, credential.Label, errDuplicateCredential)
		}
		return fmt.Errorf("create credential: %w", err)
	}
	return nil
}

// errDuplicateCredential marks a fingerprint collision within an organization.
var errDuplicateCredential = errors.New("credential already exists for this organization")

// Get returns one credential in an organization.
func (r *Credentials) Get(ctx context.Context, organizationID, credentialID string) (Credential, error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	credential, err := scanCredential(r.pool.QueryRow(ctx, `
		SELECT `+credentialColumns+` FROM credential_metadata
		WHERE organization_id = $1 AND id = $2 AND deleted_at IS NULL`,
		organizationID, credentialID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Credential{}, fmt.Errorf("credential: %w", domain.ErrNotFound)
		}
		return Credential{}, fmt.Errorf("get credential: %w", err)
	}
	return credential, nil
}

// List returns an organization's credentials, newest first.
func (r *Credentials) List(ctx context.Context, organizationID string, limit int, cursor string) (Page[Credential], error) {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	rows, err := r.pool.Query(ctx, `
		SELECT `+credentialColumns+` FROM credential_metadata
		WHERE organization_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC, id DESC LIMIT $2`,
		organizationID, int64(clampLimit(limit)+1))
	if err != nil {
		return Page[Credential]{}, fmt.Errorf("list credentials: %w", err)
	}
	defer rows.Close()
	// Fetch one extra row to know whether another page exists.
	items, err := collectCredentials(rows)
	if err != nil {
		return Page[Credential]{}, err
	}
	return pageFromRows(items, clampLimit(limit), func(c Credential) string {
		return createdCursor(c.CreatedAt, c.ID)
	}), nil
}

// Rotate replaces the encrypted value and key version atomically.
func (r *Credentials) Rotate(ctx context.Context, organizationID, credentialID string, ciphertext, nonce []byte, keyVersion int, fingerprint string) error {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	tag, err := r.pool.Exec(ctx, `
		UPDATE credential_metadata
		SET ciphertext = $3, nonce = $4, key_version = $5, fingerprint = $6,
		    status = 'active', updated_at = now()
		WHERE organization_id = $1 AND id = $2 AND deleted_at IS NULL
		  AND status <> 'revoked'`,
		organizationID, credentialID, ciphertext, nonce, keyVersion, fingerprint)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("rotate credential: %w", errDuplicateCredential)
		}
		return fmt.Errorf("rotate credential: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Distinguish "missing" from "revoked".
		credential, getErr := r.Get(ctx, organizationID, credentialID)
		if getErr != nil {
			return getErr
		}
		if credential.Status == "revoked" {
			return fmt.Errorf("credential %s is revoked: %w", credentialID, domain.ErrPermissionDenied)
		}
		return fmt.Errorf("credential %s: %w", credentialID, domain.ErrValidation)
	}
	return nil
}

// Revoke marks a credential revoked. The ciphertext is retained for audit but
// the row can no longer be rotated or read by the API.
func (r *Credentials) Revoke(ctx context.Context, organizationID, credentialID string) error {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	tag, err := r.pool.Exec(ctx, `
		UPDATE credential_metadata
		SET status = 'revoked', updated_at = now()
		WHERE organization_id = $1 AND id = $2 AND deleted_at IS NULL AND status <> 'revoked'`,
		organizationID, credentialID)
	if err != nil {
		return fmt.Errorf("revoke credential: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, getErr := r.Get(ctx, organizationID, credentialID); getErr != nil {
			return getErr
		}
		return nil // already revoked: idempotent
	}
	return nil
}

// MarkVerified records a successful verification timestamp.
func (r *Credentials) MarkVerified(ctx context.Context, organizationID, credentialID string) error {
	ctx, cancel := withQueryTimeout(ctx)
	defer cancel()
	_, err := r.pool.Exec(ctx, `
		UPDATE credential_metadata SET last_verified_at = now()
		WHERE organization_id = $1 AND id = $2`, organizationID, credentialID)
	return err
}

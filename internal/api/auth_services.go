package api

import (
	"context"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// Dependency interfaces for the M02 services. The API depends on behavior so
// handler tests can substitute fakes without a database.
type (
	sessionsStore interface {
		Create(ctx context.Context, userID, tokenHash, csrfHash, ipHash, userAgent string, ttl time.Duration) (store.Session, error)
		Get(ctx context.Context, tokenHash string) (store.Session, error)
		Touch(ctx context.Context, tokenHash string, interval time.Duration) error
		Rotate(ctx context.Context, oldHash, newHash, newCSRFHash, ipHash, userAgent string, ttl time.Duration) (store.Session, error)
		Revoke(ctx context.Context, tokenHash, reason string) error
		RevokeAllForUser(ctx context.Context, userID, reason string) (int64, error)
		CountRecentFailedAttempts(ctx context.Context, email string, since time.Duration) (int, error)
		RecordAttempt(ctx context.Context, email string, succeeded bool) error
	}

	identitiesStore interface {
		GetLocalByEmail(ctx context.Context, email string) (store.Identity, error)
		CreateLocal(ctx context.Context, userID, email, passwordHash string) error
		HasLocalIdentity(ctx context.Context) (bool, error)
	}

	membersStore interface {
		List(ctx context.Context, organizationID string, limit int) (store.Page[store.Member], error)
		CountOwners(ctx context.Context, organizationID string) (int, error)
		Get(ctx context.Context, organizationID, membershipID string) (store.Member, error)
		Invite(ctx context.Context, organizationID, email, role, displayName string) (store.Member, error)
		UpdateRole(ctx context.Context, organizationID, membershipID, role string) (store.Member, error)
		Remove(ctx context.Context, organizationID, membershipID string) error
		PermissionsFor(ctx context.Context, organizationID, userID string) ([]string, error)
		RoleFor(ctx context.Context, organizationID, userID string) (string, error)
	}

	usersStore interface {
		UpsertByEmail(ctx context.Context, email, displayName string) (string, error)
		GetByEmail(ctx context.Context, email string) (domain.User, error)
		Get(ctx context.Context, userID string) (domain.User, error)
	}

	credentialsStore interface {
		Create(ctx context.Context, credential store.Credential) error
		Get(ctx context.Context, organizationID, credentialID string) (store.Credential, error)
		List(ctx context.Context, organizationID string, limit int, cursor string) (store.Page[store.Credential], error)
		Rotate(ctx context.Context, organizationID, credentialID string, ciphertext, nonce []byte, keyVersion int, fingerprint string) error
		Revoke(ctx context.Context, organizationID, credentialID string) error
		MarkVerified(ctx context.Context, organizationID, credentialID string) error
	}
)

// authServices bundles the M02 dependencies.
type authServices struct {
	sessions      sessionsStore
	identities    identitiesStore
	members       membersStore
	users         usersStore
	organizations organizationsStore
	credentials   credentialsStore
	hasher        passwordHasher
}

// AuthServicesConfig groups the M02 repositories and the password hasher.
type AuthServicesConfig struct {
	Sessions      sessionsStore
	Identities    identitiesStore
	Members       membersStore
	Users         usersStore
	Organizations organizationsStore
	Credentials   credentialsStore
	Hasher        passwordHasher
}

// NewAuthServices builds the authentication service bundle. A nil hasher falls
// back to argon2id.
func NewAuthServices(cfg AuthServicesConfig) *authServices {
	services := &authServices{
		sessions:      cfg.Sessions,
		identities:    cfg.Identities,
		members:       cfg.Members,
		users:         cfg.Users,
		organizations: cfg.Organizations,
		credentials:   cfg.Credentials,
		hasher:        cfg.Hasher,
	}
	if services.hasher == nil {
		services.hasher = realHasher{}
	}
	return services
}

// Validate fails fast when a dependency is missing.
func (a *authServices) Validate() error {
	if a == nil || a.sessions == nil || a.identities == nil || a.members == nil ||
		a.users == nil || a.organizations == nil || a.credentials == nil {
		return errRequired("auth services")
	}
	return nil
}

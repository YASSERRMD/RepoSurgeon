package api

import (
	"context"

	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// storeSubjectDirectory resolves callers against the users and organizations
// repositories. The HTTP layer depends on the interface; this is the only
// implementation until M02 replaces the development resolver with real
// authentication.
type storeSubjectDirectory struct {
	users         *store.Users
	organizations *store.Organizations
}

// NewSubjectDirectory builds the caller resolver from the store repositories.
func NewSubjectDirectory(users *store.Users, organizations *store.Organizations) SubjectDirectory {
	return &storeSubjectDirectory{users: users, organizations: organizations}
}

// ProvisionUser upserts the caller by email and returns their id.
func (d *storeSubjectDirectory) ProvisionUser(ctx context.Context, email string) (string, error) {
	if d.users == nil {
		return "", errNoSubject
	}
	return d.users.UpsertByEmail(ctx, email, "")
}

// FirstOrganization returns the caller's first organization.
func (d *storeSubjectDirectory) FirstOrganization(ctx context.Context, userID string) (string, error) {
	if d.organizations == nil {
		return "", errNoSubject
	}
	page, err := d.organizations.ListForUser(ctx, userID, 1, "")
	if err != nil {
		return "", err
	}
	if len(page.Items) == 0 {
		return "", errNoOrganization
	}
	return page.Items[0].ID, nil
}

// Membership reports the caller's role in an organization.
func (d *storeSubjectDirectory) Membership(ctx context.Context, organizationID, userID string) (string, error) {
	if d.organizations == nil {
		return "", errNoSubject
	}
	return d.organizations.Membership(ctx, organizationID, userID)
}

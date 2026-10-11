package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
)

// Subject is the authenticated caller. M01 resolves it from the
// X-Dev-Subject header outside production; M02 replaces that resolver with
// real session authentication. OrganizationID is the caller's active
// organization, always verified against the membership table — a client can
// never assert an organization it does not belong to.
type Subject struct {
	UserID         string
	Email          string
	OrganizationID string
}

type subjectKey struct{}

// SubjectFrom returns the caller recorded on the context.
func SubjectFrom(ctx context.Context) (Subject, bool) {
	subject, ok := ctx.Value(subjectKey{}).(Subject)
	return subject, ok
}

// SubjectDirectory resolves a caller's identity and their active organization.
type SubjectDirectory interface {
	// ProvisionUser creates or updates the caller record and returns its id.
	ProvisionUser(ctx context.Context, email string) (string, error)
	// FirstOrganization returns the caller's first organization, or
	// domain.ErrNotFound when they belong to none.
	FirstOrganization(ctx context.Context, userID string) (string, error)
	// Membership returns the caller's role in an organization, or
	// domain.ErrNotFound when they are not a member.
	Membership(ctx context.Context, organizationID, userID string) (string, error)
}

// subjectResolver resolves the caller for a request.
type subjectResolver interface {
	Resolve(ctx context.Context, r *http.Request) (Subject, *Error)
}

// devSubjectResolver resolves a caller from the X-Dev-Subject header. It is
// only installed outside production: production requests must carry real
// authentication (M02), and this resolver refuses to run there.
type devSubjectResolver struct {
	env       config.Env
	directory SubjectDirectory
	log       *slog.Logger
}

// headerActiveOrganization names the optional active-organization override.
const headerActiveOrganization = "X-Active-Organization"

// Resolve returns the caller for a development request.
func (d devSubjectResolver) Resolve(ctx context.Context, r *http.Request) (Subject, *Error) {
	if d.env == config.EnvProduction {
		return Subject{}, &Error{
			Status:  http.StatusUnauthorized,
			Code:    "unauthenticated",
			Message: "authentication is required; the development subject resolver is disabled in production",
		}
	}
	email := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Dev-Subject")))
	if email == "" {
		email = "developer@localhost"
	}
	if d.directory == nil {
		return Subject{UserID: "usr_development", Email: email}, nil
	}
	userID, err := d.directory.ProvisionUser(ctx, email)
	if err != nil {
		if d.log != nil {
			d.log.Error("subject provisioning failed", "err", err)
		}
		return Subject{}, &Error{
			Status:  http.StatusUnauthorized,
			Code:    "unauthenticated",
			Message: "subject could not be provisioned",
		}
	}
	subject := Subject{UserID: userID, Email: email}
	subject.OrganizationID = d.resolveActiveOrganization(ctx, r, userID)
	return subject, nil
}

// resolveActiveOrganization honors a verified X-Active-Organization header
// and otherwise falls back to the caller's first organization.
func (d devSubjectResolver) resolveActiveOrganization(ctx context.Context, r *http.Request, userID string) string {
	if requested := strings.TrimSpace(r.Header.Get(headerActiveOrganization)); requested != "" {
		if _, err := d.directory.Membership(ctx, requested, userID); err == nil {
			return requested
		}
		if d.log != nil {
			d.log.Warn("ignoring active organization header for non-member",
				"requested", requested, "user", userID)
		}
		return ""
	}
	organizationID, err := d.directory.FirstOrganization(ctx, userID)
	if err != nil {
		return ""
	}
	return organizationID
}

// subjectMiddleware installs the resolver and guarantees every route below it
// has an authenticated subject.
func subjectMiddleware(resolver subjectResolver, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject, err := resolver.Resolve(r.Context(), r)
		if err != nil {
			WriteError(w, r, err, nil)
			return
		}
		ctx := context.WithValue(r.Context(), subjectKey{}, subject)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireSubject returns the caller or writes 401. Handlers call it so a
// missing subject can never cause a nil dereference.
func requireSubject(w http.ResponseWriter, r *http.Request) (Subject, bool) {
	subject, ok := SubjectFrom(r.Context())
	if !ok || subject.UserID == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusUnauthorized,
			Code:    "unauthenticated",
			Message: "authentication is required",
		}, nil)
		return Subject{}, false
	}
	return subject, true
}

// errNoSubject reports a caller that could not be resolved.
var errNoSubject = errors.New("api: no subject")

// requireSession writes 401 unless the request carries a live session.
func requireSession(w http.ResponseWriter, r *http.Request) {
	if _, ok := sessionFrom(r.Context()); !ok {
		WriteError(w, r, &Error{
			Status:  http.StatusUnauthorized,
			Code:    "unauthenticated",
			Message: "authentication is required",
		}, nil)
	}
}

// errNoOrganization is returned when a caller belongs to no organization yet.
var errNoOrganization = errNoSubject

package api

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/agent"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/YASSERRMD/RepoSurgeon/internal/model"
	"github.com/YASSERRMD/RepoSurgeon/internal/redact"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// Dependency interfaces. The API depends on behavior, not on concrete store
// types, so handler tests can substitute fakes without a database.
type (
	organizationsStore interface {
		Create(ctx context.Context, org domain.Organization, actorID, role string) error
		Get(ctx context.Context, organizationID string) (domain.Organization, error)
		GetBySlug(ctx context.Context, slug string) (domain.Organization, error)
		ListForUser(ctx context.Context, userID string, limit int, cursor string) (domain.Page[domain.OrganizationWithRole], error)
		Membership(ctx context.Context, organizationID, userID string) (string, error)
		SlugTaken(ctx context.Context, slug string) (bool, error)
	}

	projectsStore interface {
		Create(ctx context.Context, project domain.Project, actorID string) error
		GetForOrg(ctx context.Context, organizationID, projectID string) (domain.Project, error)
		ListForOrg(ctx context.Context, organizationID, filter string, limit int, cursor string) (domain.Page[domain.Project], error)
		UpdateForOrg(ctx context.Context, organizationID, projectID string, version int64, update domain.ProjectUpdate, actorID string) (domain.Project, error)
	}

	auditStore interface {
		Append(ctx context.Context, events []domain.AuditEvent) error
		ListForProject(ctx context.Context, organizationID, projectID, action string, limit int, cursor string) (domain.Page[domain.AuditEvent], error)
	}

	idempotencyStore interface {
		Get(ctx context.Context, key string) (status int, body map[string]any, found bool, err error)
		Store(ctx context.Context, key, organizationID, endpoint, requestHash string, status int, body map[string]any, ttl time.Duration) error
	}
)

// Services bundles every dependency the M01 handlers need.
type Services struct {
	Organizations organizationsStore
	Projects      projectsStore
	Audit         auditStore
	Idempotency   idempotencyStore
}

// NewServices builds the M01 service bundle from the store repositories.
func NewServices(orgs *store.Organizations, projects *store.Projects, audit *store.Audit, idempotency *store.Idempotency) *Services {
	return &Services{
		Organizations: orgs,
		Projects:      projects,
		Audit:         audit,
		Idempotency:   idempotency,
	}
}

// Validate fails fast at startup when a dependency is missing, so a
// misconfigured server never serves 500s at request time.
func (s *Services) Validate() error {
	var missing []string
	if s.Organizations == nil {
		missing = append(missing, "organizations")
	}
	if s.Projects == nil {
		missing = append(missing, "projects")
	}
	if s.Audit == nil {
		missing = append(missing, "audit")
	}
	if s.Idempotency == nil {
		missing = append(missing, "idempotency")
	}
	if len(missing) > 0 {
		return &missingServicesError{names: missing}
	}
	return nil
}

type missingServicesError struct{ names []string }

func (e *missingServicesError) Error() string {
	return "api: missing services: " + strings.Join(e.names, ", ")
}

// ---- request payloads ------------------------------------------------------

type organizationCreateRequest struct {
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type projectCreateRequest struct {
	OrganizationID string `json:"organization_id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
}

type projectUpdateRequest struct {
	Version     int64          `json:"version"`
	Name        *string        `json:"name"`
	Description *string        `json:"description"`
	Settings    map[string]any `json:"settings"`
}

// Limits mirrored by the OpenAPI contract.
var (
	slugPattern          = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)
	maxNameLength        = 120
	maxDescriptionLength = 2000
	maxIDLength          = 128
	maxIdempotencyKey    = 128
)

// ---- helpers ---------------------------------------------------------------

// validationError converts a domain validation failure into the canonical
// field-level error body.
func validationError(err error) *Error {
	var validation *domain.ValidationError
	if errors.As(err, &validation) {
		fields := validation.Fields
		if len(fields) == 0 {
			fields = map[string]string{"body": err.Error()}
		}
		return &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "request validation failed",
			Details: map[string]any{"fields": fields},
		}
	}
	return &Error{Status: http.StatusBadRequest, Code: "validation_failed", Message: err.Error()}
}

// conflictError maps a domain conflict onto 409 with the structured detail the
// OpenAPI contract documents.
func conflictError(err error) *Error {
	var conflict *domain.ConflictError
	if errors.As(err, &conflict) {
		details := map[string]any{"code": conflict.Code}
		if conflict.Code == domain.ConflictVersionMismatch {
			details["current_version"] = conflict.CurrentVersion
		}
		return &Error{
			Status:  http.StatusConflict,
			Code:    conflict.Code,
			Message: conflict.Message,
			Details: details,
		}
	}
	return &Error{Status: http.StatusConflict, Code: "conflict", Message: err.Error()}
}

// writeDomainError translates store and provider errors onto HTTP responses.
// Unexpected errors always collapse to a generic 500 with the reason logged,
// never sent.
func writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	if providerErr := providerErrorFor(err); providerErr != nil {
		WriteError(w, r, providerErr, nil)
		return
	}
	if errors.Is(err, agent.ErrRunNotFound) {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	if agent.IsConflict(err) {
		var conflict *agent.ConflictError
		_ = errors.As(err, &conflict)
		WriteError(w, r, &Error{
			Status:  http.StatusConflict,
			Code:    conflict.Code,
			Message: conflict.Message,
		}, nil)
		return
	}
	switch {
	case domain.IsNotFound(err):
		WriteError(w, r, ErrNotFound, nil)
	case domain.IsPermissionDenied(err):
		WriteError(w, r, &Error{
			Status:  http.StatusForbidden,
			Code:    "permission_denied",
			Message: "you do not have access to this resource",
		}, nil)
	case domain.IsConflict(err):
		WriteError(w, r, conflictError(err), nil)
	case domain.IsValidation(err):
		WriteError(w, r, validationError(err), nil)
	default:
		WriteError(w, r, ErrInternal, map[string]any{"reason": redact.String(err.Error())})
	}
}

// paginationParams reads cursor and limit from the query string, applying the
// contract bounds.
func paginationParams(r *http.Request) (int, string, *Error) {
	limit := store.DefaultPageLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := parsePositiveInt(raw)
		if err != nil {
			return 0, "", &Error{Status: http.StatusBadRequest, Code: "validation_failed", Message: "limit must be a positive integer"}
		}
		limit = parsed
	}
	if limit > store.MaxPageLimit {
		limit = store.MaxPageLimit
	}
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if len(cursor) > 512 {
		return 0, "", &Error{Status: http.StatusBadRequest, Code: "validation_failed", Message: "cursor is too long"}
	}
	return limit, cursor, nil
}

func parsePositiveInt(raw string) (int, error) {
	if raw == "" {
		return 0, errors.New("empty")
	}
	value := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, errors.New("not a positive integer")
		}
		value = value*10 + int(r-'0')
	}
	if value <= 0 {
		return 0, errors.New("must be positive")
	}
	return value, nil
}

// ---- audit -----------------------------------------------------------------

// auditRecorder writes one audit event per mutating request. A failure to
// record is logged and never fails the primary operation, because the audit
// trail is a control plane, not the user's result.
type auditRecorder struct {
	store auditStore
	log   *slog.Logger
}

func (a auditRecorder) record(ctx context.Context, subject Subject, event domain.AuditEvent) {
	if a.store == nil || subject.UserID == "" {
		return
	}
	if event.ID == "" {
		event.ID = ids.MustNew("aue")
	}
	if event.OrganizationID == "" {
		event.OrganizationID = subject.OrganizationID
	}
	if event.ActorID == "" {
		event.ActorID = subject.UserID
	}
	event.RequestID = RequestID(ctx)
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}
	if err := a.store.Append(ctx, []domain.AuditEvent{event}); err != nil && a.log != nil {
		a.log.Error("audit append failed",
			"action", event.Action,
			"request_id", event.RequestID,
			"err", redact.String(err.Error()))
	}
}

// ---- idempotency -----------------------------------------------------------

// idempotencyKeyFor extracts and validates the Idempotency-Key header.
func idempotencyKeyFor(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "idempotency_required",
			Message: "Idempotency-Key header is required for this operation",
		}, nil)
		return "", false
	}
	if len(key) > maxIdempotencyKey {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "Idempotency-Key is too long",
		}, nil)
		return "", false
	}
	return key, true
}

// requestFingerprint hashes the request body so a replayed key with different
// content is rejected instead of silently returning a stale response.
func requestFingerprint(body []byte) string {
	sum := sha256.Sum256(body)
	return base64.RawURLEncoding.EncodeToString(sum[:12])
}

// providerErrorFor maps a model provider failure onto a gateway status.
func providerErrorFor(err error) *Error {
	switch {
	case isModelError(err, model.ErrProviderRateLimit):
		return &Error{
			Status:  http.StatusTooManyRequests,
			Code:    "provider_rate_limit",
			Message: "the model provider is rate limiting this workspace",
		}
	case isModelError(err, model.ErrProviderTimeout):
		return &Error{
			Status:  http.StatusGatewayTimeout,
			Code:    "provider_timeout",
			Message: "the model provider timed out",
		}
	case isModelError(err, model.ErrProviderServer):
		return &Error{
			Status:  http.StatusBadGateway,
			Code:    "provider_error",
			Message: "the model provider reported a server error",
		}
	case isModelError(err, model.ErrProviderResponse), isModelError(err, model.ErrProviderTransport):
		return &Error{
			Status:  http.StatusBadGateway,
			Code:    "provider_error",
			Message: "the model provider response could not be processed",
		}
	}
	return nil
}

func isModelError(err, target error) bool {
	return errors.Is(err, target)
}

func newOrganizationID() string { return ids.MustNew("org") }
func newProjectID() string      { return ids.MustNew("prj") }

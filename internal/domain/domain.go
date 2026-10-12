// Package domain defines the durable entities, value types and typed errors
// shared by the API, the stores and the worker. No database or HTTP types
// leak into this package.
package domain

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// User is an authenticated principal. Email is matched case-insensitively by
// the users_email_unique_idx partial index.
type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Organization is the tenant isolation boundary. Every scoped record belongs
// to exactly one organization.
type Organization struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// OrganizationWithRole adds the caller's membership role.
type OrganizationWithRole struct {
	Organization
	Role string `json:"role"`
}

// Project groups repositories, issues and runs inside an organization.
type Project struct {
	ID             string         `json:"id"`
	OrganizationID string         `json:"organization_id"`
	Name           string         `json:"name"`
	Description    string         `json:"description"`
	Settings       map[string]any `json:"settings"`
	Version        int64          `json:"version"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
}

// ProjectUpdate is a partial update. Nil fields are left unchanged. Version is
// the caller's optimistic concurrency token and is mandatory.
type ProjectUpdate struct {
	Name        *string        `json:"name,omitempty"`
	Description *string        `json:"description,omitempty"`
	Settings    map[string]any `json:"settings,omitempty"`
}

// AuditEvent is an append-only record of a state-changing request.
type AuditEvent struct {
	ID             string         `json:"id"`
	OrganizationID string         `json:"organization_id"`
	ProjectID      string         `json:"project_id,omitempty"`
	ActorID        string         `json:"actor_id,omitempty"`
	Action         string         `json:"action"`
	EntityType     string         `json:"entity_type"`
	EntityID       string         `json:"entity_id,omitempty"`
	RequestID      string         `json:"request_id"`
	Payload        map[string]any `json:"payload"`
	Sequence       int64          `json:"sequence"`
	CreatedAt      time.Time      `json:"created_at"`
}

// Page is one keyset-paginated result set and the wire shape shared by every
// listing endpoint. NextCursor is empty when the page is the last one.
type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// Issue is an imported GitHub issue. Body is untrusted data: it is displayed
// and sent to models as content, never treated as instruction.
type Issue struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	ProjectID      string    `json:"project_id"`
	RepositoryID   string    `json:"repository_id,omitempty"`
	ExternalID     string    `json:"external_id,omitempty"`
	Number         int64     `json:"number"`
	Title          string    `json:"title"`
	Body           string    `json:"body"`
	State          string    `json:"state"`
	BodyHash       string    `json:"body_hash"`
	ETag           string    `json:"etag,omitempty"`
	Labels         []string  `json:"labels"`
	Assignees      []string  `json:"assignees"`
	CommentsCount  int       `json:"comments_count"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// HashBody returns the sha256 fingerprint of an issue body so staleness is
// detectable without storing a second copy.
func HashBody(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

// NewRunID returns a fresh run identifier.
func NewRunID() string { return "run_" + strings.ReplaceAll(uuidString(), "-", "")[:20] }

// NewPatchAttemptID returns a fresh patch attempt identifier.
func NewPatchAttemptID() string { return "pat_" + strings.ReplaceAll(uuidString(), "-", "")[:20] }

// NewCheckpointID returns a fresh checkpoint identifier.
func NewCheckpointID() string { return "chk_" + strings.ReplaceAll(uuidString(), "-", "")[:20] }

// NewPlanID returns a fresh plan identifier.
func NewPlanID() string { return "pln_" + strings.ReplaceAll(uuidString(), "-", "")[:20] }

// NewContextID returns a fresh context manifest identifier.
func NewContextID() string { return "ctx_" + strings.ReplaceAll(uuidString(), "-", "")[:20] }

// NewApprovalID returns a fresh approval identifier.
func NewApprovalID() string { return "apr_" + strings.ReplaceAll(uuidString(), "-", "")[:20] }

// NewTaskID returns a fresh task identifier.
func NewTaskID() string { return "tsk_" + strings.ReplaceAll(uuidString(), "-", "")[:20] }

// NewInstallationID returns a fresh installation binding identifier.
func NewInstallationID() string { return "ghi_" + strings.ReplaceAll(uuidString(), "-", "")[:20] }

// NewIssueID returns a fresh issue identifier.
func NewIssueID() string { return "iss_" + strings.ReplaceAll(uuidString(), "-", "")[:20] }

// Queue sentinels.
var (
	// ErrNoTaskAvailable is returned when a claim finds nothing to work on.
	ErrNoTaskAvailable = errors.New("queue: no task available")
)

// Membership roles, enforced by the database check constraint and by the
// policy layer.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
	RoleViewer = "viewer"
)

// Project settings keys with product meaning. Unknown keys are rejected so
// settings cannot smuggle arbitrary data into the workspace.
const (
	SettingDefaultBranch         = "default_branch"
	SettingExternalPRAllowed     = "external_pr_allowed"
	SettingMergeRequiresApproval = "merge_requires_approval"
	SettingModelProvider         = "model_provider"
	SettingModelID               = "model_id"
)

// KnownSettings lists the accepted settings keys and their types.
var KnownSettings = map[string]string{
	SettingDefaultBranch:         "string",
	SettingExternalPRAllowed:     "boolean",
	SettingMergeRequiresApproval: "boolean",
	SettingModelProvider:         "string",
	SettingModelID:               "string",
}

// uuidString returns a random UUID-shaped string without an external package.
func uuidString() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("domain: crypto/rand unavailable: " + err.Error())
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	const hexDigits = "0123456789abcdef"
	out := make([]byte, 0, 36)
	for i, b := range raw {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out = append(out, '-')
		}
		out = append(out, hexDigits[b>>4], hexDigits[b&0x0f])
	}
	return string(out)
}

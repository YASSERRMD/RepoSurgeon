// Package domain defines the durable entities, value types and typed errors
// shared by the API, the stores and the worker. No database or HTTP types
// leak into this package.
package domain

import "time"

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

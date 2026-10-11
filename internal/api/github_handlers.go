package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/github"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// Dependency interfaces so handler tests can substitute fakes.
type (
	installationsStore interface {
		Connect(ctx context.Context, binding store.GitHubInstallation) error
		List(ctx context.Context, organizationID string) ([]store.GitHubInstallation, error)
		Get(ctx context.Context, organizationID string, installationID int64) (store.GitHubInstallation, error)
		MarkRemoved(ctx context.Context, organizationID string, installationID int64) error
	}

	webhookDeliveriesStore interface {
		Record(ctx context.Context, delivery store.WebhookDelivery) (bool, error)
	}

	issuesStore interface {
		Import(ctx context.Context, record store.IssueRecord) (store.IssueRecord, error)
		ListForProject(ctx context.Context, projectID string, limit int, cursor string) (store.Page[store.IssueRecord], error)
		GetForProject(ctx context.Context, projectID, issueID string) (store.IssueRecord, error)
	}
)

// GitHubServices bundles the M04 GitHub dependencies. It is exported so
// cmd/api can construct it from configuration.
type GitHubServices struct {
	installations installationsStore
	deliveries    webhookDeliveriesStore
	issues        issuesStore
	projects      projectsStore
	adapter       *github.Adapter
	verifier      *github.SignatureVerifier
}

// GitHubServicesConfig configures the GitHub adapter.
type GitHubServicesConfig struct {
	Installations *store.GitHubInstallations
	Deliveries    *store.WebhookDeliveries
	Issues        *store.Issues
	Projects      projectsStore
	AppID         int64
	PrivateKeyPEM []byte
	APIBaseURL    string
	WebhookSecret string
}

// NewGitHubServices builds the GitHub service bundle. It returns nil when the
// App or webhook configuration is absent, which disables the GitHub routes.
func NewGitHubServices(cfg GitHubServicesConfig) *GitHubServices {
	var adapter *github.Adapter
	if cfg.AppID != 0 && len(cfg.PrivateKeyPEM) > 0 {
		built, err := github.NewAdapter(github.Config{
			AppID:         cfg.AppID,
			PrivateKeyPEM: cfg.PrivateKeyPEM,
			APIBaseURL:    cfg.APIBaseURL,
		})
		if err == nil {
			adapter = built
		}
	}
	var verifier *github.SignatureVerifier
	if strings.TrimSpace(cfg.WebhookSecret) != "" {
		if built, err := github.NewSignatureVerifier(cfg.WebhookSecret); err == nil {
			verifier = built
		}
	}
	if adapter == nil || verifier == nil {
		return nil
	}
	return &GitHubServices{
		installations: cfg.Installations,
		deliveries:    cfg.Deliveries,
		issues:        cfg.Issues,
		projects:      cfg.Projects,
		adapter:       adapter,
		verifier:      verifier,
	}
}

// Configured reports whether both the adapter and verifier are ready.
func (g *GitHubServices) Configured() bool {
	return g != nil && g.adapter != nil && g.verifier != nil
}

// handleConnectInstallation binds a GitHub App installation to the caller's
// organization. The installation ID is verified against GitHub before the
// binding is stored.
func (s *Server) handleConnectInstallation(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "credential.admin") {
		return
	}
	if s.githubApp == nil || s.githubApp.adapter == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "github_not_configured",
			Message: "the GitHub App is not configured on this server",
		}, nil)
		return
	}

	var payload struct {
		InstallationID int64  `json:"installation_id"`
		AccountLogin   string `json:"account_login"`
	}
	if !decodeInto(w, r, &payload) {
		return
	}
	if payload.InstallationID <= 0 {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "installation_id must be a positive integer",
		}, nil)
		return
	}

	// Verify the installation exists and is reachable with our App credentials.
	repos, err := s.githubApp.adapter.InstallationRepositories(r.Context(), payload.InstallationID)
	if err != nil {
		s.audit(r, session.Subject, domain.AuditEvent{
			OrganizationID: session.OrganizationID,
			Action:         "github.install_failed",
			EntityType:     "installation",
			EntityID:       strconv.FormatInt(payload.InstallationID, 10),
			Payload:        map[string]any{"error": githubErrorCode(err)},
		})
		writeDomainError(w, r, err)
		return
	}
	account := strings.TrimSpace(payload.AccountLogin)
	if account == "" && len(repos) > 0 {
		account = repos[0].FullName
		if at := strings.Index(account, "/"); at > 0 {
			account = account[:at]
		}
	}

	binding := store.GitHubInstallation{
		ID:             domain.NewInstallationID(),
		OrganizationID: session.OrganizationID,
		InstallationID: payload.InstallationID,
		AccountLogin:   account,
		CreatedBy:      session.UserID,
	}
	if err := s.githubApp.installations.Connect(r.Context(), binding); err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		Action:         "github.installation_connected",
		EntityType:     "installation",
		EntityID:       strconv.FormatInt(payload.InstallationID, 10),
		Payload:        map[string]any{"account": account, "repositories": len(repos)},
	})
	WriteJSON(w, http.StatusCreated, map[string]any{
		"installation_id": binding.InstallationID,
		"account_login":   binding.AccountLogin,
		"repositories":    len(repos),
	})
}

// handleListGitHubRepos lists repositories the installation can access.
func (s *Server) handleListGitHubRepos(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "credential.admin") {
		return
	}
	if s.githubApp == nil || s.githubApp.adapter == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "github_not_configured",
			Message: "the GitHub App is not configured on this server",
		}, nil)
		return
	}
	installationID, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("installation_id")), 10, 64)
	if err != nil || installationID <= 0 {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "installation_id query parameter must be a positive integer",
		}, nil)
		return
	}
	// The installation must be bound to the caller's organization.
	if _, err := s.githubApp.installations.Get(r.Context(), session.OrganizationID, installationID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	repos, err := s.githubApp.adapter.InstallationRepositories(r.Context(), installationID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(repos))
	for _, repo := range repos {
		items = append(items, map[string]any{
			"id":             repo.ID,
			"full_name":      repo.FullName,
			"name":           repo.Name,
			"default_branch": repo.DefaultBranch,
			"private":        repo.Private,
			"archived":       repo.Archived,
			"html_url":       repo.HTMLURL,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleImportIssue imports one GitHub issue into a project by URL.
func (s *Server) handleImportIssue(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.write") {
		return
	}
	if s.githubApp == nil || s.githubApp.adapter == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "github_not_configured",
			Message: "the GitHub App is not configured on this server",
		}, nil)
		return
	}

	var payload struct {
		ProjectID      string `json:"project_id"`
		InstallationID int64  `json:"installation_id"`
		IssueURL       string `json:"issue_url"`
	}
	if !decodeInto(w, r, &payload) {
		return
	}
	owner, repo, number, err := parseIssueURL(payload.IssueURL)
	if err != nil {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: err.Error(),
		}, nil)
		return
	}
	project, err := s.githubApp.projects.GetForOrg(r.Context(), session.OrganizationID, payload.ProjectID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if _, err := s.githubApp.installations.Get(r.Context(), session.OrganizationID, payload.InstallationID); err != nil {
		writeDomainError(w, r, err)
		return
	}

	issue, err := s.githubApp.adapter.GetIssue(r.Context(), payload.InstallationID, owner, repo, number)
	if err != nil {
		s.audit(r, session.Subject, domain.AuditEvent{
			OrganizationID: session.OrganizationID,
			ProjectID:      project.ID,
			Action:         "github.import_failed",
			EntityType:     "issue",
			Payload:        map[string]any{"error": githubErrorCode(err)},
		})
		writeDomainError(w, r, err)
		return
	}
	comments, err := s.githubApp.adapter.ListIssueComments(r.Context(), payload.InstallationID, owner, repo, number)
	if err != nil {
		s.log.Warn("issue comments unavailable", "err", err)
	}

	record := store.IssueRecord{
		Issue: domain.Issue{
			ID:             domain.NewIssueID(),
			OrganizationID: session.OrganizationID,
			ProjectID:      project.ID,
			Number:         issue.Number,
			Title:          issue.Title,
			Body:           issue.Body,
			State:          issue.State,
			BodyHash:       domain.HashBody(issue.Body),
			CommentsCount:  len(comments),
			CreatedAt:      time.Now().UTC(),
			UpdatedAt:      time.Now().UTC(),
		},
		ImportedBy: session.UserID,
	}
	stored, err := s.githubApp.issues.Import(r.Context(), record)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      project.ID,
		Action:         "github.issue_imported",
		EntityType:     "issue",
		EntityID:       stored.ID,
		Payload: map[string]any{
			"number":    stored.Number,
			"url":       payload.IssueURL,
			"comments":  stored.CommentsCount,
			"body_hash": stored.BodyHash,
		},
	})
	WriteJSON(w, http.StatusCreated, issuePayload(stored))
}

// handleGitHubWebhook verifies, de-duplicates and records a delivery.
func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if s.githubApp == nil || s.githubApp.verifier == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "github_not_configured",
			Message: "the GitHub webhook secret is not configured on this server",
		}, nil)
		return
	}
	body := readWebhookBody(w, r)
	if body == nil {
		return
	}
	deliveryID, err := s.githubApp.verifier.Verify(r.Header, body)
	if err != nil {
		// A replay of an authentic delivery is acknowledged, not punished: the
		// signature was valid, the delivery was simply already processed.
		if errors.Is(err, github.ErrReplayedDelivery) {
			s.log.Info("webhook replay ignored", "delivery", deliveryID)
			WriteJSON(w, http.StatusOK, map[string]any{"status": "duplicate"})
			return
		}
		s.log.Warn("webhook rejected", "reason", err.Error())
		WriteError(w, r, &Error{
			Status:  http.StatusUnauthorized,
			Code:    "invalid_signature",
			Message: "webhook signature verification failed",
		}, nil)
		return
	}

	event, action, err := github.ParseEvent(body)
	if err != nil {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "webhook payload is not valid JSON",
		}, nil)
		return
	}
	sum := sha256.Sum256(body)
	alreadySeen, err := s.githubApp.deliveries.Record(r.Context(), store.WebhookDelivery{
		ID:             deliveryID,
		InstallationID: installationOf(event),
		EventType:      github.EventType(r.Header),
		Action:         action,
		PayloadHash:    hex.EncodeToString(sum[:16]),
	})
	if err != nil {
		s.log.Error("record webhook delivery", "err", err)
		WriteError(w, r, ErrInternal, nil)
		return
	}
	if alreadySeen {
		WriteJSON(w, http.StatusOK, map[string]any{"status": "duplicate"})
		return
	}
	if action == "deleted" || action == "suspend" {
		s.log.Info("installation lifecycle event", "action", action, "delivery", deliveryID)
	}
	s.log.Info("webhook recorded", "delivery", deliveryID, "event", github.EventType(r.Header), "action", action)
	WriteJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "delivery_id": deliveryID})
}

func installationOf(event github.Event) int64 {
	if event.Installation == nil {
		return 0
	}
	return event.Installation.ID
}

// handleListIssues lists a project's imported issues.
func (s *Server) handleListIssues(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	if projectID == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "project_id query parameter is required",
		}, nil)
		return
	}
	if _, err := s.githubApp.projects.GetForOrg(r.Context(), session.OrganizationID, projectID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	limit, cursor, paramErr := paginationParams(r)
	if paramErr != nil {
		WriteError(w, r, paramErr, nil)
		return
	}
	page, err := s.githubApp.issues.ListForProject(r.Context(), projectID, limit, cursor)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, record := range page.Items {
		items = append(items, issuePayload(record))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}

// readWebhookBody reads and returns the webhook body, writing 413 when it is
// too large.
func readWebhookBody(w http.ResponseWriter, r *http.Request) []byte {
	limited := http.MaxBytesReader(w, r.Body, github.MaxWebhookBodySize)
	body, err := io.ReadAll(limited)
	if err != nil {
		WriteError(w, r, &Error{
			Status:  http.StatusRequestEntityTooLarge,
			Code:    "payload_too_large",
			Message: "webhook payload is too large",
		}, nil)
		return nil
	}
	if len(body) == 0 {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "webhook body is required",
		}, nil)
		return nil
	}
	return body
}

// parseIssueURL extracts owner, repo and number from a GitHub issue URL.
func parseIssueURL(raw string) (owner, repo string, number int64, err error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", 0, errValidation("issue_url is required")
	}
	path := trimmed
	if at := strings.Index(path, "://"); at >= 0 {
		path = path[at+3:]
		if slash := strings.Index(path, "/"); slash >= 0 {
			path = path[slash+1:]
		}
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// owner/repo/issues/<number>
	if len(parts) < 4 || parts[2] != "issues" {
		return "", "", 0, errValidation("issue_url must look like https://github.com/owner/repo/issues/123")
	}
	number, convErr := strconv.ParseInt(parts[3], 10, 64)
	if convErr != nil || number <= 0 {
		return "", "", 0, errValidation("issue_url must end with a positive issue number")
	}
	return parts[0], parts[1], number, nil
}

func issuePayload(record store.IssueRecord) map[string]any {
	return map[string]any{
		"id":             record.ID,
		"project_id":     record.ProjectID,
		"number":         record.Number,
		"title":          record.Title,
		"body":           record.Body,
		"state":          record.State,
		"labels":         record.Labels,
		"assignees":      record.Assignees,
		"comments_count": record.CommentsCount,
		"body_hash":      record.BodyHash,
		"created_at":     record.CreatedAt,
		"updated_at":     record.UpdatedAt,
	}
}

func githubErrorCode(err error) string {
	var apiErr *github.APIError
	if asGitHubError(err, &apiErr) {
		switch {
		case apiErr.RateLimited():
			return "github_rate_limit"
		case apiErr.PermissionDenied():
			return "github_permission_denied"
		default:
			return "github_api_error"
		}
	}
	return "github_error"
}

func asGitHubError(err error, target **github.APIError) bool {
	if apiErr, ok := err.(*github.APIError); ok {
		*target = apiErr
		return true
	}
	return false
}

func errValidation(message string) error {
	return &domainValidationError{message: message}
}

type domainValidationError struct{ message string }

func (e *domainValidationError) Error() string { return e.message }

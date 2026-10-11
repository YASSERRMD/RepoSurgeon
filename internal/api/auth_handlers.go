package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/auth"
	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/YASSERRMD/RepoSurgeon/internal/secrets"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// ---- login / logout / refresh ---------------------------------------------

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// handleLogin authenticates a local identity and starts a session.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var payload loginRequest
	if !decodeInto(w, r, &payload) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(payload.Email))
	if email == "" || !strings.Contains(email, "@") {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "a valid email address is required",
		}, nil)
		return
	}
	if len(payload.Password) < auth.MinPasswordLength {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "password must be at least 12 characters",
		}, nil)
		return
	}

	// Brute-force control: identical response for unknown accounts so the
	// endpoint cannot be used to enumerate users.
	failures, err := s.auth.sessions.CountRecentFailedAttempts(r.Context(), email, loginLockoutWindow)
	if err != nil {
		s.log.Error("count login attempts", "err", err)
		WriteError(w, r, ErrInternal, nil)
		return
	}
	if failures >= maxLoginFailures {
		s.log.Warn("login rate limited", "email_domain", emailDomain(email), "failures", failures)
		_ = s.auth.sessions.RecordAttempt(r.Context(), email, false)
		WriteError(w, r, &Error{
			Status:  http.StatusTooManyRequests,
			Code:    "rate_limited",
			Message: "too many failed login attempts; try again later",
		}, nil)
		return
	}

	identity, err := s.auth.identities.GetLocalByEmail(r.Context(), email)
	if err != nil || identity.PasswordHash == "" {
		_ = s.auth.sessions.RecordAttempt(r.Context(), email, false)
		s.log.Info("login rejected", "reason", "unknown_identity_or_missing_password", "email_domain", emailDomain(email))
		WriteError(w, r, &Error{
			Status:  http.StatusUnauthorized,
			Code:    "invalid_credentials",
			Message: "email or password is incorrect",
		}, nil)
		return
	}
	ok, err := s.auth.hasher.Verify(identity.PasswordHash, payload.Password)
	if err != nil {
		s.log.Error("verify password", "err", err)
		WriteError(w, r, ErrInternal, nil)
		return
	}
	if !ok {
		_ = s.auth.sessions.RecordAttempt(r.Context(), email, false)
		s.audit(r, Subject{UserID: identity.UserID}, domain.AuditEvent{
			OrganizationID: "",
			Action:         "auth.login_failed",
			EntityType:     "user",
			EntityID:       identity.UserID,
			Payload:        map[string]any{"reason": "invalid_password"},
		})
		WriteError(w, r, &Error{
			Status:  http.StatusUnauthorized,
			Code:    "invalid_credentials",
			Message: "email or password is incorrect",
		}, nil)
		return
	}

	_ = s.auth.sessions.RecordAttempt(r.Context(), email, true)
	subject, err := s.loadSubject(r.Context(), identity.UserID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		WriteError(w, r, ErrInternal, nil)
		return
	}
	csrfToken, csrfHash, err := auth.NewCSRFToken()
	if err != nil {
		WriteError(w, r, ErrInternal, nil)
		return
	}
	session, err := s.auth.sessions.Create(r.Context(), identity.UserID, hash, csrfHash, authIPHash(r), r.UserAgent(), store.SessionTTL)
	if err != nil {
		s.log.Error("create session", "err", err)
		WriteError(w, r, ErrInternal, nil)
		return
	}
	setSessionCookie(w, token, time.Until(session.ExpiresAt), s.cfg.Env)
	s.audit(r, subject, domain.AuditEvent{
		OrganizationID: subject.OrganizationID,
		Action:         "auth.login",
		EntityType:     "user",
		EntityID:       subject.UserID,
	})
	WriteJSON(w, http.StatusOK, map[string]any{
		"user":       userSummary(subject),
		"csrf_token": csrfToken,
		"expires_at": session.ExpiresAt,
	})
}

// handleLogout revokes the current session.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	subject, ok := sessionFrom(r.Context())
	if !ok {
		WriteError(w, r, &Error{
			Status:  http.StatusUnauthorized,
			Code:    "unauthenticated",
			Message: "authentication is required",
		}, nil)
		return
	}
	if err := s.auth.sessions.Revoke(r.Context(), subject.SessionHash, "logout"); err != nil {
		s.log.Error("revoke session", "err", err)
	}
	clearSessionCookie(w)
	s.audit(r, subject.Subject, domain.AuditEvent{
		OrganizationID: subject.OrganizationID,
		Action:         "auth.logout",
		EntityType:     "user",
		EntityID:       subject.UserID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// handleRefresh rotates the current session, revoking the presented token.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	subject, ok := sessionFrom(r.Context())
	if !ok {
		WriteError(w, r, &Error{
			Status:  http.StatusUnauthorized,
			Code:    "unauthenticated",
			Message: "authentication is required",
		}, nil)
		return
	}
	token, hash, err := auth.NewToken()
	if err != nil {
		WriteError(w, r, ErrInternal, nil)
		return
	}
	csrfToken, csrfHash, err := auth.NewCSRFToken()
	if err != nil {
		WriteError(w, r, ErrInternal, nil)
		return
	}
	session, err := s.auth.sessions.Rotate(r.Context(), subject.SessionHash, hash, csrfHash, authIPHash(r), r.UserAgent(), store.SessionTTL)
	if err != nil {
		if domain.IsNotFound(err) {
			WriteError(w, r, &Error{
				Status:  http.StatusUnauthorized,
				Code:    "unauthenticated",
				Message: "session is no longer valid",
			}, nil)
			return
		}
		s.log.Error("rotate session", "err", err)
		WriteError(w, r, ErrInternal, nil)
		return
	}
	setSessionCookie(w, token, time.Until(session.ExpiresAt), s.cfg.Env)
	WriteJSON(w, http.StatusOK, map[string]any{
		"user":       userSummary(subject.Subject),
		"csrf_token": csrfToken,
		"expires_at": session.ExpiresAt,
	})
}

// handleCurrentUser returns the caller with roles and permissions.
func (s *Server) handleCurrentUser(w http.ResponseWriter, r *http.Request) {
	subject, ok := sessionFrom(r.Context())
	if !ok {
		WriteError(w, r, &Error{
			Status:  http.StatusUnauthorized,
			Code:    "unauthenticated",
			Message: "authentication is required",
		}, nil)
		return
	}
	page, err := s.services.Organizations.ListForUser(r.Context(), subject.UserID, 100, "")
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	organizations := make([]map[string]any, 0, len(page.Items))
	for _, org := range page.Items {
		permissions, err := s.auth.members.PermissionsFor(r.Context(), org.ID, subject.UserID)
		if err != nil {
			writeDomainError(w, r, err)
			return
		}
		organizations = append(organizations, map[string]any{
			"id":          org.ID,
			"name":        org.Name,
			"slug":        org.Slug,
			"role":        org.Role,
			"permissions": permissions,
		})
	}
	summary := userSummary(subject.Subject)
	summary["organizations"] = organizations
	WriteJSON(w, http.StatusOK, summary)
}

// ---- development bootstrap -------------------------------------------------

// handleBootstrap creates the first local user and organization. It is the
// only way to obtain an account without an OIDC provider and is disabled as
// soon as any local identity exists.
func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Env == config.EnvProduction {
		WriteError(w, r, &Error{
			Status:  http.StatusForbidden,
			Code:    "bootstrap_disabled",
			Message: "bootstrap is disabled in production; provision accounts with your identity provider",
		}, nil)
		return
	}
	exists, err := s.auth.identities.HasLocalIdentity(r.Context())
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if exists {
		WriteError(w, r, &Error{
			Status:  http.StatusConflict,
			Code:    "bootstrap_completed",
			Message: "an account already exists; log in instead",
		}, nil)
		return
	}

	var payload struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
		OrgName     string `json:"organization_name"`
	}
	if !decodeInto(w, r, &payload) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(payload.Email))
	fields := map[string]string{}
	if email == "" || !strings.Contains(email, "@") {
		fields["email"] = "a valid email address is required"
	}
	if len(payload.Password) < auth.MinPasswordLength {
		fields["password"] = "password must be at least 12 characters"
	}
	if len(fields) > 0 {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "request validation failed",
			Details: map[string]any{"fields": fields},
		}, nil)
		return
	}

	hash, err := s.auth.hasher.Hash(payload.Password)
	if err != nil {
		if errors.Is(err, auth.ErrPasswordTooShort) {
			WriteError(w, r, &Error{Status: http.StatusBadRequest, Code: "validation_failed", Message: err.Error()}, nil)
			return
		}
		WriteError(w, r, ErrInternal, nil)
		return
	}

	displayName := strings.TrimSpace(payload.DisplayName)
	if displayName == "" {
		displayName = email
	}
	if _, err := s.auth.users.UpsertByEmail(r.Context(), email, displayName); err != nil {
		writeDomainError(w, r, err)
		return
	}
	stored, err := s.auth.users.GetByEmail(r.Context(), email)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	userID := stored.ID
	if err := s.auth.identities.CreateLocal(r.Context(), userID, email, hash); err != nil {
		writeDomainError(w, r, err)
		return
	}

	orgName := strings.TrimSpace(payload.OrgName)
	if orgName == "" {
		orgName = displayName + "'s organization"
	}
	org := domain.Organization{
		ID:        ids.MustNew("org"),
		Slug:      ids.MustNew("org"),
		Name:      orgName,
		CreatedAt: time.Now().UTC(),
	}
	org.UpdatedAt = org.CreatedAt
	if err := s.services.Organizations.Create(r.Context(), org, userID, domain.RoleOwner); err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, Subject{UserID: userID, OrganizationID: org.ID}, domain.AuditEvent{
		OrganizationID: org.ID,
		Action:         "auth.bootstrap",
		EntityType:     "user",
		EntityID:       userID,
	})
	WriteJSON(w, http.StatusCreated, map[string]any{
		"user":         map[string]any{"id": userID, "email": email, "display_name": displayName},
		"organization": map[string]any{"id": org.ID, "name": org.Name, "slug": org.Slug},
	})
}

// ---- helpers ---------------------------------------------------------------

func userSummary(subject Subject) map[string]any {
	return map[string]any{
		"id":           subject.UserID,
		"email":        subject.Email,
		"display_name": subject.Email,
	}
}

func emailDomain(email string) string {
	at := strings.Index(email, "@")
	if at < 0 || at == len(email)-1 {
		return "unknown"
	}
	return email[at+1:]
}

// ---- members and credentials ----------------------------------------------

type memberInviteRequest struct {
	OrganizationID string `json:"organization_id"`
	Email          string `json:"email"`
	Role           string `json:"role"`
}

// handleListMembers lists an organization's members.
func (s *Server) handleListMembers(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	organizationID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if organizationID == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "organization_id query parameter is required",
		}, nil)
		return
	}
	if _, err := s.auth.members.RoleFor(r.Context(), organizationID, session.UserID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	scoped := session
	scoped.OrganizationID = organizationID
	if !s.checkPermission(w, r, scoped, "project.read") {
		return
	}
	page, err := s.auth.members.List(r.Context(), organizationID, 100)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, member := range page.Items {
		items = append(items, map[string]any{
			"id":           member.ID,
			"user_id":      member.UserID,
			"email":        member.Email,
			"display_name": member.DisplayName,
			"role":         member.Role,
			"created_at":   member.CreatedAt,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": ""})
}

// handleInviteMember adds or updates a membership.
func (s *Server) handleInviteMember(w http.ResponseWriter, r *http.Request) {
	s.idempotentSession(w, r, "POST /api/v1/members", func(session sessionSubject) (int, any) {
		if !s.checkPermission(w, r, session, "member.admin") {
			return http.StatusForbidden, nil
		}
		var payload memberInviteRequest
		if !decodeInto(w, r, &payload) {
			return http.StatusBadRequest, nil
		}
		email := strings.ToLower(strings.TrimSpace(payload.Email))
		if email == "" || !strings.Contains(email, "@") {
			WriteError(w, r, &Error{
				Status:  http.StatusBadRequest,
				Code:    "validation_failed",
				Message: "a valid email address is required",
				Details: map[string]any{"fields": map[string]string{"email": "invalid email"}},
			}, nil)
			return http.StatusBadRequest, nil
		}
		if !isKnownRole(payload.Role) {
			WriteError(w, r, &Error{
				Status:  http.StatusBadRequest,
				Code:    "validation_failed",
				Message: "role must be one of owner, admin, maintainer, reviewer, member, viewer",
			}, nil)
			return http.StatusBadRequest, nil
		}
		member, err := s.auth.members.Invite(r.Context(), payload.OrganizationID, email, payload.Role, email)
		if err != nil {
			writeDomainError(w, r, err)
			return http.StatusInternalServerError, nil
		}
		s.audit(r, session.Subject, domain.AuditEvent{
			OrganizationID: payload.OrganizationID,
			Action:         "member.invited",
			EntityType:     "membership",
			EntityID:       member.ID,
			Payload:        map[string]any{"email": emailDomain(email), "role": payload.Role},
		})
		return http.StatusCreated, memberPayload(member)
	})
}

// handleUpdateMemberRole changes a member's role.
func (s *Server) handleUpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFrom(r.Context())
	if session.UserID == "" {
		requireSession(w, r)
		return
	}
	s.idempotentSession(w, r, "PATCH /api/v1/members/{membership_id}", func(subject sessionSubject) (int, any) {
		if !s.checkPermission(w, r, session, "member.admin") {
			return http.StatusForbidden, nil
		}
		membershipID := Params(r.Context())["membership_id"]
		var payload struct {
			Role string `json:"role"`
		}
		if !decodeInto(w, r, &payload) {
			return http.StatusBadRequest, nil
		}
		if !isKnownRole(payload.Role) {
			WriteError(w, r, &Error{
				Status:  http.StatusBadRequest,
				Code:    "validation_failed",
				Message: "role must be one of owner, admin, maintainer, reviewer, member, viewer",
			}, nil)
			return http.StatusBadRequest, nil
		}
		member, err := s.auth.members.Get(r.Context(), subject.OrganizationID, membershipID)
		if err != nil {
			writeDomainError(w, r, err)
			return http.StatusNotFound, nil
		}
		// Nobody may escalate their own role, and the last owner is protected.
		if member.UserID == subject.UserID {
			WriteError(w, r, &Error{
				Status:  http.StatusConflict,
				Code:    "self_escalation_forbidden",
				Message: "you cannot change your own role",
			}, nil)
			return http.StatusConflict, nil
		}
		if member.Role == domain.RoleOwner && payload.Role != domain.RoleOwner {
			owners, err := s.auth.members.CountOwners(r.Context(), subject.OrganizationID)
			if err != nil {
				writeDomainError(w, r, err)
				return http.StatusInternalServerError, nil
			}
			if owners <= 1 {
				WriteError(w, r, &Error{
					Status:  http.StatusConflict,
					Code:    "last_owner_protected",
					Message: "the organization must keep at least one owner",
				}, nil)
				return http.StatusConflict, nil
			}
		}
		updated, err := s.auth.members.UpdateRole(r.Context(), subject.OrganizationID, membershipID, payload.Role)
		if err != nil {
			writeDomainError(w, r, err)
			return http.StatusInternalServerError, nil
		}
		s.audit(r, subject.Subject, domain.AuditEvent{
			OrganizationID: subject.OrganizationID,
			Action:         "member.role_updated",
			EntityType:     "membership",
			EntityID:       membershipID,
			Payload:        map[string]any{"from": member.Role, "to": payload.Role},
		})
		return http.StatusOK, memberPayload(updated)
	})
}

// handleRemoveMember removes a membership.
func (s *Server) handleRemoveMember(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "member.admin") {
		return
	}
	membershipID := Params(r.Context())["membership_id"]
	member, err := s.auth.members.Get(r.Context(), session.OrganizationID, membershipID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if member.Role == domain.RoleOwner {
		owners, err := s.auth.members.CountOwners(r.Context(), session.OrganizationID)
		if err != nil {
			writeDomainError(w, r, err)
			return
		}
		if owners <= 1 {
			WriteError(w, r, &Error{
				Status:  http.StatusConflict,
				Code:    "last_owner_protected",
				Message: "the organization must keep at least one owner",
			}, nil)
			return
		}
	}
	if err := s.auth.members.Remove(r.Context(), session.OrganizationID, membershipID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		Action:         "member.removed",
		EntityType:     "membership",
		EntityID:       membershipID,
	})
	w.WriteHeader(http.StatusNoContent)
}

func memberPayload(member store.Member) map[string]any {
	return map[string]any{
		"id":           member.ID,
		"user_id":      member.UserID,
		"email":        member.Email,
		"display_name": member.DisplayName,
		"role":         member.Role,
		"created_at":   member.CreatedAt,
	}
}

func isKnownRole(role string) bool {
	switch role {
	case domain.RoleOwner, domain.RoleAdmin, "maintainer", "reviewer", domain.RoleMember, domain.RoleViewer:
		return true
	}
	return false
}

// ---- credentials -----------------------------------------------------------

type credentialCreateRequest struct {
	OrganizationID string `json:"organization_id"`
	Kind           string `json:"kind"`
	Label          string `json:"label"`
	Value          string `json:"value"`
}

func (s *Server) handleListCredentials(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	organizationID := strings.TrimSpace(r.URL.Query().Get("organization_id"))
	if organizationID == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "organization_id query parameter is required",
		}, nil)
		return
	}
	if _, err := s.auth.members.RoleFor(r.Context(), organizationID, session.UserID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	page, err := s.auth.credentials.List(r.Context(), organizationID, 100, "")
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, credential := range page.Items {
		items = append(items, credentialPayload(credential))
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
}

func (s *Server) handleCreateCredential(w http.ResponseWriter, r *http.Request) {
	s.idempotentSession(w, r, "POST /api/v1/credentials", func(subject sessionSubject) (int, any) {
		if !s.checkPermission(w, r, subject, "credential.admin") {
			return http.StatusForbidden, nil
		}
		var payload credentialCreateRequest
		if !decodeInto(w, r, &payload) {
			return http.StatusBadRequest, nil
		}
		kind := strings.TrimSpace(payload.Kind)
		label := strings.TrimSpace(payload.Label)
		fields := map[string]string{}
		if !isKnownCredentialKind(kind) {
			fields["kind"] = "kind must be one of github_app, stepfun, other"
		}
		if label == "" || len(label) > 120 {
			fields["label"] = "label is required and must be at most 120 characters"
		}
		if len(payload.Value) < 8 || len(payload.Value) > 4096 {
			fields["value"] = "value must be between 8 and 4096 characters"
		}
		if len(fields) > 0 {
			WriteError(w, r, &Error{
				Status:  http.StatusBadRequest,
				Code:    "validation_failed",
				Message: "request validation failed",
				Details: map[string]any{"fields": fields},
			}, nil)
			return http.StatusBadRequest, nil
		}

		if s.secrets == nil {
			WriteError(w, r, &Error{
				Status:  http.StatusServiceUnavailable,
				Code:    "encryption_unavailable",
				Message: "credential encryption is not configured on this server",
			}, nil)
			return http.StatusServiceUnavailable, nil
		}
		ciphertext, nonce, err := s.secrets.EncryptSecret([]byte(payload.Value))
		if err != nil {
			s.log.Error("encrypt credential", "err", err)
			WriteError(w, r, ErrInternal, nil)
			return http.StatusInternalServerError, nil
		}
		credential := store.Credential{
			ID:             ids.MustNew("crd"),
			OrganizationID: payload.OrganizationID,
			Kind:           kind,
			Label:          label,
			Status:         "active",
			Ciphertext:     ciphertext,
			Nonce:          nonce,
			KeyVersion:     credentialKeyVersionOf(s.secrets),
			Fingerprint:    secrets.Fingerprint(payload.Value),
			CreatedBy:      subject.UserID,
			CreatedAt:      time.Now().UTC(),
		}
		credential.UpdatedAt = credential.CreatedAt
		if err := s.auth.credentials.Create(r.Context(), credential); err != nil {
			writeDomainError(w, r, err)
			if domain.IsValidation(err) {
				return http.StatusBadRequest, nil
			}
			return http.StatusInternalServerError, nil
		}
		s.audit(r, subject.Subject, domain.AuditEvent{
			OrganizationID: credential.OrganizationID,
			Action:         "credential.created",
			EntityType:     "credential",
			EntityID:       credential.ID,
			Payload:        map[string]any{"kind": kind, "label": label, "key_version": credential.KeyVersion},
		})
		return http.StatusCreated, credentialPayload(credential)
	})
}

func (s *Server) handleGetCredential(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	credential, err := s.auth.credentials.Get(r.Context(), session.OrganizationID, Params(r.Context())["credential_id"])
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, credentialPayload(credential))
}

func (s *Server) handleRotateCredential(w http.ResponseWriter, r *http.Request) {
	s.idempotentSession(w, r, "POST /api/v1/credentials/{credential_id}/rotate", func(subject sessionSubject) (int, any) {
		if !s.checkPermission(w, r, subject, "credential.admin") {
			return http.StatusForbidden, nil
		}
		credentialID := Params(r.Context())["credential_id"]
		var payload struct {
			Value string `json:"value"`
		}
		if !decodeInto(w, r, &payload) {
			return http.StatusBadRequest, nil
		}
		if len(payload.Value) < 8 || len(payload.Value) > 4096 {
			WriteError(w, r, &Error{
				Status:  http.StatusBadRequest,
				Code:    "validation_failed",
				Message: "value must be between 8 and 4096 characters",
			}, nil)
			return http.StatusBadRequest, nil
		}
		current, err := s.auth.credentials.Get(r.Context(), subject.OrganizationID, credentialID)
		if err != nil {
			writeDomainError(w, r, err)
			return http.StatusNotFound, nil
		}
		if s.secrets == nil {
			WriteError(w, r, &Error{
				Status:  http.StatusServiceUnavailable,
				Code:    "encryption_unavailable",
				Message: "credential encryption is not configured on this server",
			}, nil)
			return http.StatusServiceUnavailable, nil
		}
		if s.secrets == nil {
			WriteError(w, r, &Error{
				Status:  http.StatusServiceUnavailable,
				Code:    "encryption_unavailable",
				Message: "credential encryption is not configured on this server",
			}, nil)
			return http.StatusServiceUnavailable, nil
		}
		ciphertext, nonce, err := s.secrets.EncryptSecret([]byte(payload.Value))
		if err != nil {
			s.log.Error("encrypt rotated credential", "err", err)
			WriteError(w, r, ErrInternal, nil)
			return http.StatusInternalServerError, nil
		}
		if err := s.auth.credentials.Rotate(r.Context(), subject.OrganizationID, credentialID,
			ciphertext, nonce, credentialKeyVersionOf(s.secrets), secrets.Fingerprint(payload.Value)); err != nil {
			writeDomainError(w, r, err)
			return http.StatusInternalServerError, nil
		}
		s.audit(r, subject.Subject, domain.AuditEvent{
			OrganizationID: subject.OrganizationID,
			Action:         "credential.rotated",
			EntityType:     "credential",
			EntityID:       credentialID,
			Payload: map[string]any{
				"previous_key_version": current.KeyVersion,
				"new_key_version":      s.secrets.KeyVersion(),
			},
		})
		updated, err := s.auth.credentials.Get(r.Context(), subject.OrganizationID, credentialID)
		if err != nil {
			writeDomainError(w, r, err)
			return http.StatusInternalServerError, nil
		}
		return http.StatusOK, credentialPayload(updated)
	})
}

func (s *Server) handleRevokeCredential(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFrom(r.Context())
	if session.UserID == "" {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "credential.admin") {
		return
	}
	credentialID := Params(r.Context())["credential_id"]
	if err := s.auth.credentials.Revoke(r.Context(), session.OrganizationID, credentialID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		Action:         "credential.revoked",
		EntityType:     "credential",
		EntityID:       credentialID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// credentialPayload is the metadata projection. The encrypted value is never
// included.
func credentialPayload(credential store.Credential) map[string]any {
	lastVerified := credential.LastVerifiedAt
	lastVerifiedValue := any(nil)
	if lastVerified != nil {
		lastVerifiedValue = *lastVerified
	}
	return map[string]any{
		"id":               credential.ID,
		"organization_id":  credential.OrganizationID,
		"kind":             credential.Kind,
		"label":            credential.Label,
		"status":           credential.Status,
		"key_version":      credential.KeyVersion,
		"fingerprint":      credential.Fingerprint,
		"last_verified_at": lastVerifiedValue,
		"created_at":       credential.CreatedAt,
	}
}

// credentialKeyVersionOf reports the configured key version, or 0 when
// encryption is unavailable.
func credentialKeyVersionOf(encryptor *secrets.Encryptor) int {
	if encryptor == nil {
		return 0
	}
	return encryptor.KeyVersion()
}

func isKnownCredentialKind(kind string) bool {
	switch kind {
	case "github_app", "stepfun", "other":
		return true
	}
	return false
}

var _ = json.Marshal

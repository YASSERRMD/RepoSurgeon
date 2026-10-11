package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/auth"
	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
)

// Session names for cookies and headers.
const (
	sessionCookieName  = "rs_session"
	csrfHeaderName     = "X-CSRF-Token"
	bearerPrefix       = "Bearer "
	sessionHeaderName  = "Authorization"
	maxLoginFailures   = 5
	loginLockoutWindow = 15 * time.Minute
)

// passwordHasher abstracts password hashing so handler tests can use a fast
// deterministic hasher.
type passwordHasher interface {
	Hash(password string) (string, error)
	Verify(encoded, password string) (bool, error)
}

// realHasher delegates to the argon2id implementation.
type realHasher struct{}

func (realHasher) Hash(password string) (string, error) { return auth.HashPassword(password) }
func (realHasher) Verify(encoded, password string) (bool, error) {
	return auth.VerifyPassword(encoded, password)
}

// sessionSubject is the authenticated caller carried on the request context.
type sessionSubject struct {
	Subject
	SessionHash string
	CSRFHash    string
}

type sessionKey struct{}

// sessionFrom returns the caller recorded by the session middleware.
func sessionFrom(ctx context.Context) (sessionSubject, bool) {
	subject, ok := ctx.Value(sessionKey{}).(sessionSubject)
	return subject, ok
}

// ---- middleware ------------------------------------------------------------

// sessionMiddleware resolves the caller from the session cookie or a bearer
// token and installs it on the request context. Anonymous requests to the
// authenticated surface are rejected by makeSessionRequired below.
func (s *Server) sessionMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := sessionTokenFrom(r)
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		hash := auth.HashToken(decodeToken(token))
		session, err := s.auth.sessions.Get(r.Context(), hash)
		if err != nil {
			// Unknown, expired or revoked token: treat as anonymous.
			clearSessionCookie(w)
			next.ServeHTTP(w, r)
			return
		}
		_ = s.auth.sessions.Touch(r.Context(), hash, time.Minute)

		subject, err := s.loadSubject(r.Context(), session.UserID)
		if err != nil {
			s.log.Error("load subject for session", "err", err)
			WriteError(w, r, ErrInternal, nil)
			return
		}
		ctx := context.WithValue(r.Context(), sessionKey{}, sessionSubject{
			Subject:     subject,
			SessionHash: hash,
			CSRFHash:    session.CSRFHash,
		})
		// Also install the M01 subject key so existing handlers work.
		ctx = context.WithValue(ctx, subjectKey{}, subject)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requireSession rejects anonymous requests to the authenticated surface.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := sessionFrom(r.Context()); !ok {
			WriteError(w, r, &Error{
				Status:  http.StatusUnauthorized,
				Code:    "unauthenticated",
				Message: "authentication is required",
			}, nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// csrfMiddleware enforces the double-submit token for cookie-authenticated
// mutations. Bearer-authenticated requests are exempt because they cannot be
// forged by a browser.
func (s *Server) csrfMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutatingMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		subject, ok := sessionFrom(r.Context())
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		if bearerAuthenticated(r) {
			next.ServeHTTP(w, r)
			return
		}
		presented := strings.TrimSpace(r.Header.Get(csrfHeaderName))
		if presented == "" {
			WriteError(w, r, &Error{
				Status:  http.StatusForbidden,
				Code:    "csrf_token_missing",
				Message: "X-CSRF-Token header is required for this request",
			}, nil)
			return
		}
		if !auth.ConstantTimeEqual(auth.HashCSRFToken(presented), subject.CSRFHash) {
			WriteError(w, r, &Error{
				Status:  http.StatusForbidden,
				Code:    "csrf_token_invalid",
				Message: "X-CSRF-Token does not match this session",
			}, nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isMutatingMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func bearerAuthenticated(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get(sessionHeaderName), bearerPrefix)
}

// sessionTokenFrom reads the token from the cookie or the Authorization
// header.
func sessionTokenFrom(r *http.Request) string {
	if header := r.Header.Get(sessionHeaderName); strings.HasPrefix(header, bearerPrefix) {
		return strings.TrimSpace(strings.TrimPrefix(header, bearerPrefix))
	}
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		return cookie.Value
	}
	return ""
}

func decodeToken(token string) []byte {
	raw, err := base64RawURLDecode(token)
	if err != nil {
		return []byte(token)
	}
	return raw
}

// setSessionCookie installs the session cookie. Secure is enabled outside
// development so the cookie never travels over plain HTTP in production.
func setSessionCookie(w http.ResponseWriter, token string, ttl time.Duration, env config.Env) {
	// Secure is enabled outside development so the cookie never travels over
	// plain HTTP; HttpOnly and SameSite=Strict are always set.
	// #nosec G124 -- HttpOnly/SameSite/Secure are set explicitly below.
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   env != config.EnvDevelopment && env != config.EnvTest,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(ttl.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	// #nosec G124 -- HttpOnly is set; SameSite defaults are applied by the browser.
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
}

// loadSubject loads the caller and their active organization.
func (s *Server) loadSubject(ctx context.Context, userID string) (Subject, error) {
	user, err := s.auth.users.Get(ctx, userID)
	if err != nil {
		return Subject{}, err
	}
	subject := Subject{UserID: user.ID, Email: user.Email}
	page, err := s.auth.organizations.ListForUser(ctx, userID, 1, "")
	if err != nil {
		return Subject{}, err
	}
	if len(page.Items) > 0 {
		subject.OrganizationID = page.Items[0].ID
	}
	return subject, nil
}

// authIPHash produces a truncated, salted-looking hash of the client address
// so brute-force controls work per client without storing raw addresses.
func authIPHash(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	mac := hmac.New(sha256.New, []byte("reposurgeon-auth"))
	mac.Write([]byte(host))
	return hex.EncodeToString(mac.Sum(nil)[:8])
}

// checkPermission verifies one permission for the session's active
// organization and writes the 403 (plus an audit record) when it is missing.
// Handlers call it before performing any privileged mutation.
func (s *Server) checkPermission(w http.ResponseWriter, r *http.Request, session sessionSubject, permission string) bool {
	if session.OrganizationID == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusForbidden,
			Code:    "permission_denied",
			Message: "you do not belong to an organization yet",
		}, nil)
		return false
	}
	granted, err := s.auth.members.PermissionsFor(r.Context(), session.OrganizationID, session.UserID)
	if err != nil {
		writeDomainError(w, r, err)
		return false
	}
	if containsString(granted, permission) {
		return true
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		Action:         "permission.denied",
		EntityType:     "permission",
		EntityID:       permission,
		Payload:        map[string]any{"path": r.URL.Path},
	})
	WriteError(w, r, &Error{
		Status:  http.StatusForbidden,
		Code:    "permission_denied",
		Message: "your role does not grant " + permission,
	}, nil)
	return false
}

// base64RawURLDecode decodes a raw URL-safe base64 token.
func base64RawURLDecode(token string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(token)
}

// containsString reports whether values contains want.
func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

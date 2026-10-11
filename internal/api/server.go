// Package api wires the RepoSurgeon HTTP surface. Handlers stay thin: business
// rules live in domain packages, and every response uses the canonical JSON
// envelope from api.WriteJSON / api.WriteError.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/health"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/secrets"
)

// Options constructs a Server.
type Options struct {
	Config *config.Config
	DB     health.Database
	// PendingMigrations reports migrations on disk that are not yet applied.
	// The wiring in cmd/api passes migrations.Pending bound to the pool; tests
	// pass a fake.
	PendingMigrations func(ctx context.Context) ([]migrations.Migration, error)
	// Services carries the M01 repositories. It is optional in M00-only
	// tests; every M01 route requires it.
	Services *Services
	// SubjectDirectory resolves callers for the subject middleware. It is
	// superseded by session authentication in M02 and stays for
	// backward-compatible wiring.
	SubjectDirectory SubjectDirectory
	// Auth carries the M02 authentication services.
	Auth *authServices
	// Encryptor seals provider credentials.
	Encryptor *secrets.Encryptor
	// Gateway is the M03 model provider surface; nil disables its routes.
	Gateway *ModelGateway
	// GitHubApp carries the M04 GitHub adapter and webhook verifier; nil
	// disables the GitHub routes.
	GitHubApp *GitHubServices
	Logger    *slog.Logger
	Version   string
	GitCommit string
	BuildTime string
}

// Server holds the dependencies shared by every route.
type Server struct {
	cfg       *config.Config
	db        health.Database
	pendingFn func(ctx context.Context) ([]migrations.Migration, error)
	services  *Services
	directory SubjectDirectory
	auth      *authServices
	secrets   *secrets.Encryptor
	gateway   *ModelGateway
	githubApp *GitHubServices
	log       *slog.Logger
	version   string
	gitCommit string
	buildTime string
	startedAt time.Time
	checker   *health.Checker
}

// NewServer validates options and returns a ready server.
func NewServer(opts Options) (*Server, error) {
	if opts.Config == nil {
		return nil, errRequired("config")
	}
	if opts.DB == nil {
		return nil, errRequired("db")
	}
	if opts.PendingMigrations == nil {
		return nil, errRequired("pending migrations")
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	if opts.Services != nil {
		if err := opts.Services.Validate(); err != nil {
			return nil, err
		}
	}
	// Auth and Encryptor are optional for M00-only servers (health/system
	// info). Every M02 route is registered only when they are present.
	if opts.Auth != nil {
		if err := opts.Auth.Validate(); err != nil {
			return nil, err
		}
	}
	s := &Server{
		cfg:       opts.Config,
		db:        opts.DB,
		pendingFn: opts.PendingMigrations,
		services:  opts.Services,
		directory: opts.SubjectDirectory,
		auth:      opts.Auth,
		secrets:   opts.Encryptor,
		gateway:   opts.Gateway,
		githubApp: opts.GitHubApp,
		log:       log,
		version:   orDefault(opts.Version, "dev"),
		gitCommit: orDefault(opts.GitCommit, "none"),
		buildTime: orDefault(opts.BuildTime, "unknown"),
		startedAt: time.Now().UTC(),
	}
	s.checker = &health.Checker{
		DB:                s.db,
		PendingMigrations: opts.PendingMigrations,
	}
	return s, nil
}

// serveAPI runs the given handler under the development subject resolver when
// M02 session authentication is not wired (tests and M01-only servers).
func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request, handler, fallback http.Handler) {
	if s.auth == nil {
		if s.directory != nil {
			subjectMiddleware(devSubjectResolver{
				env:       s.cfg.Env,
				directory: s.directory,
				log:       s.log,
			}, handler).ServeHTTP(w, r)
			return
		}
		final := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := SubjectFrom(r.Context()); !ok {
				ctx := context.WithValue(r.Context(), subjectKey{}, Subject{UserID: "usr_development"})
				handler.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			handler.ServeHTTP(w, r)
		}))
		final.ServeHTTP(w, r)
		return
	}
	handler.ServeHTTP(w, r)
}

// isPublicAuthPath reports whether a path is reachable without a session.
func isPublicAuthPath(path string) bool {
	switch path {
	case "/api/v1/auth/login", "/api/v1/auth/bootstrap", "/api/v1/system/info":
		return true
	}
	return false
}

// Handler returns the fully wrapped HTTP handler.
func (s *Server) Handler() http.Handler {
	router := NewRouter()
	s.registerRoutes(router)
	// The subject middleware wraps only the authenticated API surface. Health
	// and system-info routes must keep working while the database is down —
	// readiness is exactly what reports that outage — so they never depend on
	// provisioning a caller.
	var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Health and system-info routes never resolve a subject: they must
		// keep answering while the database is down.
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			router.ServeHTTP(w, r)
			return
		}
		// M00/M01 servers without authentication services keep the
		// development subject behavior.
		if s.auth == nil {
			s.serveAPI(w, r, router, http.NotFoundHandler())
			return
		}
		if isPublicAuthPath(r.URL.Path) {
			s.serveAPI(w, r, router, http.NotFoundHandler())
			return
		}
		// Webhooks are server-to-server: they carry a signature instead of a
		// session, so they bypass the session middleware.
		if r.URL.Path == "/api/v1/webhooks/github" {
			router.ServeHTTP(w, r)
			return
		}
		// M02: authenticated surface with CSRF protection for mutations.
		var authenticated http.Handler
		switch r.URL.Path {
		case "/api/v1/auth/login", "/api/v1/auth/bootstrap", "/api/v1/auth/logout", "/api/v1/auth/refresh":
			// These endpoints establish or rotate the session, so they are only
			// wrapped by the session resolver.
			authenticated = s.sessionMiddleware(router)
		default:
			authenticated = s.sessionMiddleware(s.csrfMiddleware(s.requireSession(router)))
		}
		authenticated.ServeHTTP(w, r)
	})
	h = corsMiddleware(s.cfg.CORSAllowedOrigins, h)
	h = securityHeadersMiddleware(h)
	h = recoverMiddleware(s.log, h)
	h = loggingMiddleware(s.log, h)
	h = requestIDMiddleware(h)
	return h
}

func (s *Server) registerRoutes(router *Router) {
	router.Handle(http.MethodGet, "/health/live", s.handleLive)
	router.Handle(http.MethodGet, "/health/ready", s.handleReady)
	router.Handle(http.MethodGet, "/api/v1/system/info", s.handleSystemInfo)
	// M01 routes require the M01 services; M02 routes the M02 services.
	if s.services == nil {
		return
	}
	if s.auth != nil {
		router.Handle(http.MethodPost, "/api/v1/auth/login", s.handleLogin)
		router.Handle(http.MethodPost, "/api/v1/auth/logout", s.handleLogout)
		router.Handle(http.MethodPost, "/api/v1/auth/refresh", s.handleRefresh)
		router.Handle(http.MethodPost, "/api/v1/auth/bootstrap", s.handleBootstrap)
		router.Handle(http.MethodGet, "/api/v1/me", s.handleCurrentUser)
	}
	router.Handle(http.MethodGet, "/api/v1/organizations", s.handleListOrganizations)
	router.Handle(http.MethodPost, "/api/v1/organizations", s.handleCreateOrganization)
	router.Handle(http.MethodGet, "/api/v1/projects", s.handleListProjects)
	router.Handle(http.MethodPost, "/api/v1/projects", s.handleCreateProject)
	router.Handle(http.MethodGet, "/api/v1/projects/{project_id}", s.handleGetProject)
	router.Handle(http.MethodPatch, "/api/v1/projects/{project_id}", s.handleUpdateProject)
	router.Handle(http.MethodGet, "/api/v1/audit", s.handleListAuditEvents)
	if s.gateway != nil {
		router.Handle(http.MethodGet, "/api/v1/model/provider", s.handleModelStatus)
		router.Handle(http.MethodPost, "/api/v1/model/probe", s.handleModelProbe)
	}
	if s.githubApp != nil {
		router.Handle(http.MethodPost, "/api/v1/github/installations/connect", s.handleConnectInstallation)
		router.Handle(http.MethodGet, "/api/v1/github/repos", s.handleListGitHubRepos)
		router.Handle(http.MethodPost, "/api/v1/issues/import", s.handleImportIssue)
		router.Handle(http.MethodPost, "/api/v1/webhooks/github", s.handleGitHubWebhook)
		router.Handle(http.MethodGet, "/api/v1/issues", s.handleListIssues)
	}
	if s.auth == nil {
		return
	}
	router.Handle(http.MethodGet, "/api/v1/members", s.handleListMembers)
	router.Handle(http.MethodPost, "/api/v1/members", s.handleInviteMember)
	router.Handle(http.MethodPatch, "/api/v1/members/{membership_id}", s.handleUpdateMemberRole)
	router.Handle(http.MethodDelete, "/api/v1/members/{membership_id}", s.handleRemoveMember)
	router.Handle(http.MethodGet, "/api/v1/credentials", s.handleListCredentials)
	router.Handle(http.MethodPost, "/api/v1/credentials", s.handleCreateCredential)
	router.Handle(http.MethodGet, "/api/v1/credentials/{credential_id}", s.handleGetCredential)
	router.Handle(http.MethodPost, "/api/v1/credentials/{credential_id}/rotate", s.handleRotateCredential)
	router.Handle(http.MethodDelete, "/api/v1/credentials/{credential_id}", s.handleRevokeCredential)
}

func errRequired(name string) error {
	return &missingOptionError{option: name}
}

type missingOptionError struct{ option string }

func (e *missingOptionError) Error() string { return "api: server option " + e.option + " is required" }

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// GoVersion is exposed for diagnostics in tests.
func GoVersion() string { return runtime.Version() }

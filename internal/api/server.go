// Package api wires the RepoSurgeon HTTP surface. Handlers stay thin: business
// rules live in domain packages, and every response uses the canonical JSON
// envelope from api.WriteJSON / api.WriteError.
package api

import (
	"context"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/health"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
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
	// SubjectDirectory resolves callers for the subject middleware.
	SubjectDirectory SubjectDirectory
	Logger           *slog.Logger
	Version          string
	GitCommit        string
	BuildTime        string
}

// Server holds the dependencies shared by every route.
type Server struct {
	cfg       *config.Config
	db        health.Database
	pendingFn func(ctx context.Context) ([]migrations.Migration, error)
	services  *Services
	directory SubjectDirectory
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
	s := &Server{
		cfg:       opts.Config,
		db:        opts.DB,
		pendingFn: opts.PendingMigrations,
		services:  opts.Services,
		directory: opts.SubjectDirectory,
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

// Handler returns the fully wrapped HTTP handler.
func (s *Server) Handler() http.Handler {
	router := NewRouter()
	s.registerRoutes(router)
	var h http.Handler = router
	// M01 routes sit behind the subject middleware so every handler can rely
	// on an authenticated, organization-verified caller.
	if s.directory != nil {
		h = subjectMiddleware(devSubjectResolver{
			env:       s.cfg.Env,
			directory: s.directory,
			log:       s.log,
		}, h)
	}
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
	if s.services == nil {
		return
	}
	router.Handle(http.MethodGet, "/api/v1/organizations", s.handleListOrganizations)
	router.Handle(http.MethodPost, "/api/v1/organizations", s.handleCreateOrganization)
	router.Handle(http.MethodGet, "/api/v1/projects", s.handleListProjects)
	router.Handle(http.MethodPost, "/api/v1/projects", s.handleCreateProject)
	router.Handle(http.MethodGet, "/api/v1/projects/{project_id}", s.handleGetProject)
	router.Handle(http.MethodPatch, "/api/v1/projects/{project_id}", s.handleUpdateProject)
	router.Handle(http.MethodGet, "/api/v1/audit", s.handleListAuditEvents)
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

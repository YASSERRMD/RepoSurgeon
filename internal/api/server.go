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
	"github.com/YASSERRMD/RepoSurgeon/internal/gitworkspace"
	"github.com/YASSERRMD/RepoSurgeon/internal/health"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/secrets"
	"github.com/YASSERRMD/RepoSurgeon/internal/telemetry"
	"go.opentelemetry.io/otel/trace"
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
	// Workspaces is the M05 workspace manager; nil disables its routes.
	Workspaces *gitworkspace.Manager
	// Runs and Queue carry the M06 durable run state machine.
	Runs  RunScheduler
	Queue TaskQueueService
	// Tools exposes the M07 catalog, policy and approval gateway.
	Tools *ToolService
	// Context exposes the M08 index, context builder and manifest store.
	Context *ContextService
	// Plans exposes the M09 plan service.
	Plans *PlanService
	// Patches exposes the M10 patch engine and checkpoints.
	Patches *PatchService
	// Checks exposes the M11 check profiles, runner and store.
	Checks *CheckService
	// Recovery exposes the M12 repair loop, journal and rollback.
	Recovery *RecoveryService
	// PRs exposes the M13 pull-request lifecycle.
	PRs *PRService
	// Console exposes the M14 dashboard aggregate and view settings.
	Console ConsoleStores
	// Events exposes the M15 run-event log and SSE stream.
	Events EventsStore
	// Cost exposes the M15 measured usage and cost aggregate.
	Cost CostStore
	// Telemetry carries the M15 OpenTelemetry provider and instruments.
	Telemetry telemetry.Provider
	// Instruments are the M15 metric instruments.
	Instruments *telemetry.Instruments
	Logger      *slog.Logger
	Version     string
	GitCommit   string
	BuildTime   string
}

// Server holds the dependencies shared by every route.
type Server struct {
	cfg         *config.Config
	db          health.Database
	pendingFn   func(ctx context.Context) ([]migrations.Migration, error)
	services    *Services
	directory   SubjectDirectory
	auth        *authServices
	secrets     *secrets.Encryptor
	gateway     *ModelGateway
	githubApp   *GitHubServices
	workspaces  *gitworkspace.Manager
	runs        RunScheduler
	queue       TaskQueueService
	tools       *ToolService
	context     *ContextService
	plans       *PlanService
	patches     *PatchService
	checks      *CheckService
	recovery    *RecoveryService
	prs         *PRService
	console     ConsoleStores
	events      EventsStore
	cost        CostStore
	telemetry   telemetry.Provider
	instruments *telemetry.Instruments

	// permissionResolver resolves the caller's permissions; it is replaced in
	// tests.
	permissionResolver func(ctx context.Context, session sessionSubject) []string
	log                *slog.Logger
	version            string
	gitCommit          string
	buildTime          string
	startedAt          time.Time
	checker            *health.Checker
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
		cfg:         opts.Config,
		db:          opts.DB,
		pendingFn:   opts.PendingMigrations,
		services:    opts.Services,
		directory:   opts.SubjectDirectory,
		auth:        opts.Auth,
		secrets:     opts.Encryptor,
		gateway:     opts.Gateway,
		githubApp:   opts.GitHubApp,
		workspaces:  opts.Workspaces,
		runs:        opts.Runs,
		queue:       opts.Queue,
		tools:       opts.Tools,
		context:     opts.Context,
		plans:       opts.Plans,
		patches:     opts.Patches,
		checks:      opts.Checks,
		recovery:    opts.Recovery,
		prs:         opts.PRs,
		console:     opts.Console,
		events:      opts.Events,
		cost:        opts.Cost,
		telemetry:   opts.Telemetry,
		instruments: opts.Instruments,

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
	if s.instruments != nil {
		h = s.telemetryMiddleware(h)
	}
	return h
}

// telemetryMiddleware wraps the router with OpenTelemetry spans and metrics
// for every request: a span per request, plus request count, latency and
// error counters.
func (s *Server) telemetryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ctx, span := telemetry.StartSpan(r.Context(), "http "+r.Method+" "+r.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
		)
		defer span.End()

		// Wrap the response writer to capture the status code.
		rw := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rw, r.WithContext(ctx))

		latency := time.Since(start)
		telemetry.RecordRequest(s.instruments, r.Context(), r.Method, r.URL.Path, rw.status, latency)
		span.SetAttributes(telemetry.SpanAttrs(r.Method, r.URL.Path, rw.status)...)
	})
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
	if s.workspaces != nil {
		router.Handle(http.MethodPost, "/api/v1/workspaces", s.handleCreateWorkspace)
		router.Handle(http.MethodDelete, "/api/v1/workspaces/{workspace_id}", s.handleDestroyWorkspace)
	}
	s.permissionResolver = func(ctx context.Context, session sessionSubject) []string {
		if s.auth == nil || s.auth.members == nil {
			return nil
		}
		granted, err := s.auth.members.PermissionsFor(ctx, session.OrganizationID, session.UserID)
		if err != nil {
			return nil
		}
		return granted
	}
	if s.context != nil {
		router.Handle(http.MethodGet, "/api/v1/workspaces/{workspace_id}/search", s.handleWorkspaceSearch)
		router.Handle(http.MethodGet, "/api/v1/workspaces/{workspace_id}/files", s.handleWorkspaceFiles)
		router.Handle(http.MethodGet, "/api/v1/workspaces/{workspace_id}/symbols", s.handleWorkspaceSymbols)
	}
	if s.workspaces != nil {
		router.Handle(http.MethodGet, "/api/v1/workspaces/{workspace_id}/tree", s.handleWorkspaceTree)
	}
	if s.tools != nil {
		router.Handle(http.MethodGet, "/api/v1/tools", s.handleListTools)
		router.Handle(http.MethodGet, "/api/v1/approvals", s.handleListApprovals)
		router.Handle(http.MethodPost, "/api/v1/approvals", s.handleCreateApprovalRequest)
		router.Handle(http.MethodPost, "/api/v1/approvals/{approval_id}/decide", s.handleDecideApproval)
	}
	if s.runs != nil {
		router.Handle(http.MethodPost, "/api/v1/runs/{run_id}/recover", s.handleRecover)
		router.Handle(http.MethodGet, "/api/v1/runs/{run_id}/recovery", s.handleGetRecovery)
		router.Handle(http.MethodPost, "/api/v1/runs/{run_id}/rollback", s.handleRollback)
	}
	router.Handle(http.MethodGet, "/api/v1/dashboard", s.handleGetDashboard)
	router.Handle(http.MethodGet, "/api/v1/me/preferences", s.handleGetPreferences)
	router.Handle(http.MethodPut, "/api/v1/me/preferences", s.handleSavePreferences)
	if s.events != nil && s.runs != nil {
		router.Handle(http.MethodGet, "/api/v1/runs/{run_id}/events", s.handleListRunEvents)
		router.Handle(http.MethodGet, "/api/v1/runs/{run_id}/events/stream", s.handleStreamRunEvents)
	}
	if s.cost != nil {
		router.Handle(http.MethodGet, "/api/v1/metrics/cost", s.handleGetCost)
		router.Handle(http.MethodGet, "/api/v1/alerts", s.handleGetAlerts)
	}
	if s.runs != nil {
		router.Handle(http.MethodPost, "/api/v1/runs/{run_id}/pr", s.handleOpenPullRequest)
		router.Handle(http.MethodGet, "/api/v1/runs/{run_id}/pr", s.handleGetPullRequest)
		router.Handle(http.MethodPost, "/api/v1/runs/{run_id}/merge", s.handleMergePullRequest)
		router.Handle(http.MethodPost, "/api/v1/runs/{run_id}/abandon", s.handleAbandonPullRequest)
	}
	if s.runs != nil {
		router.Handle(http.MethodPost, "/api/v1/runs/{run_id}/checks", s.handleRunChecks)
		router.Handle(http.MethodGet, "/api/v1/runs/{run_id}/checks", s.handleListChecks)
		router.Handle(http.MethodGet, "/api/v1/runs/{run_id}/checks/{check_id}/logs", s.handleGetCheckLogs)
	}
	if s.runs != nil {
		router.Handle(http.MethodPost, "/api/v1/runs/{run_id}/patches", s.handleApplyPatch)
		router.Handle(http.MethodGet, "/api/v1/runs/{run_id}/diff", s.handleGetDiff)
		router.Handle(http.MethodPost, "/api/v1/runs/{run_id}/checkpoints/{checkpoint_id}/restore", s.handleRestoreCheckpoint)
	}
	if s.runs != nil {
		router.Handle(http.MethodPost, "/api/v1/runs/{run_id}/plan", s.handleSavePlan)
		router.Handle(http.MethodGet, "/api/v1/runs/{run_id}/plan", s.handleGetPlan)
		router.Handle(http.MethodPost, "/api/v1/runs/{run_id}/plan/approve", s.handleApprovePlan)
	}
	if s.queue != nil {
		router.Handle(http.MethodGet, "/api/v1/runs/{run_id}/context", s.handleRunContext)
		router.Handle(http.MethodPost, "/api/v1/runs/{run_id}/context/refresh", s.handleRunContextRefresh)
		router.Handle(http.MethodPost, "/api/v1/runs", s.handleCreateRun)
		router.Handle(http.MethodGet, "/api/v1/runs", s.handleListRuns)
		router.Handle(http.MethodGet, "/api/v1/runs/{run_id}", s.handleGetRun)
		router.Handle(http.MethodPost, "/api/v1/runs/{run_id}/start", s.handleStartRun)
		router.Handle(http.MethodPost, "/api/v1/runs/{run_id}/cancel", s.handleCancelRun)
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

// Command api serves the RepoSurgeon HTTP API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/api"
	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/contextbuild"
	"github.com/YASSERRMD/RepoSurgeon/internal/gitworkspace"
	"github.com/YASSERRMD/RepoSurgeon/internal/index"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/queue"
	"github.com/YASSERRMD/RepoSurgeon/internal/secrets"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// Build metadata injected with -ldflags -X.
var (
	version   = "dev"
	gitCommit = "none"
	buildTime = "unknown"
)

func main() {
	if err := run(); err != nil {
		slog.Error("api exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	migrateOnly := flag.Bool("migrate-only", false, "apply pending migrations and exit")
	flag.Parse()

	// A migrate-only run never touches encrypted credentials, so it must not
	// require the credential key.
	cfg, err := config.LoadFrom(os.LookupEnv, config.LoadOptions{
		RequireDatabase:      true,
		RequireCredentialKey: !*migrateOnly,
	})
	if err != nil {
		return err
	}
	logger := newLogger(cfg)
	logger.Info("api starting", "config", cfg.Redacted(), "version", version, "commit", gitCommit)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := store.Open(ctx, cfg.DatabaseURL, store.DefaultOptions())
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.MigrateOnStart || *migrateOnly {
		applied, err := migrations.Up(ctx, pool, cfg.MigrationsDir)
		if err != nil {
			return err
		}
		for _, mg := range applied {
			logger.Info("migration applied", "version", mg.Version, "name", mg.Name)
		}
		if *migrateOnly {
			logger.Info("migrate-only complete", "applied", len(applied))
			return nil
		}
	}

	services := api.NewServices(
		store.NewOrganizations(pool),
		store.NewProjects(pool),
		store.NewAudit(pool),
		store.NewIdempotency(pool),
	)
	// Migrations never touch encrypted credentials, so a migrate-only run
	// skips building the encryptor entirely.
	var encryptor *secrets.Encryptor
	if !*migrateOnly {
		built, buildErr := secrets.New(cfg.CredentialKEK, cfg.CredentialKeyVersion)
		if buildErr != nil {
			return fmt.Errorf("credential encryption: %w", buildErr)
		}
		encryptor = built
	}
	var gateway *api.ModelGateway
	if built, buildErr := api.NewModelGateway(cfg.Model()); buildErr != nil {
		logger.Warn("model gateway disabled", "err", buildErr)
	} else {
		gateway = built
		logger.Info("model gateway configured",
			"provider", "stepfun",
			"model", cfg.DefaultModelID,
			"key_configured", cfg.StepFunAPIKey != "")
	}

	// M04: GitHub adapter and webhook verifier are optional; both require
	// configuration that is unavailable in development by default.
	var githubApp *api.GitHubServices
	if built := api.NewGitHubServices(api.GitHubServicesConfig{
		Installations: store.NewGitHubInstallations(pool),
		Deliveries:    store.NewWebhookDeliveries(pool),
		Issues:        store.NewIssues(pool),
		Projects:      store.NewProjects(pool),
		AppID:         githubAppID(cfg),
		PrivateKeyPEM: githubPrivateKey(cfg),
		APIBaseURL:    cfg.GitHubAPIBaseURL,
		WebhookSecret: cfg.GitHubWebhookSecret,
	}); built != nil && built.Configured() {
		githubApp = built
		logger.Info("github integration configured",
			"app_id", githubAppID(cfg), "api_base_url", cfg.GitHubAPIBaseURL)
	} else {
		logger.Info("github integration disabled (app credentials or webhook secret absent)")
	}

	// M05: pinned checkouts and worktrees live under a dedicated root.
	workspaces, err := gitworkspace.New(cfg.WorkspaceRoot)
	if err != nil {
		return fmt.Errorf("workspace manager: %w", err)
	}

	// M06: durable run state machine and task queue.
	runsRepo := store.NewRuns(pool)
	taskQueue := queue.New(pool)

	// M07: tool catalog, policy and approval gateway.
	toolService := api.NewToolService(api.NewMemoryApprovals())

	// M08: repository index, context builder and in-memory manifest store.
	repoIndex := index.New(nil, cfg.WorkspaceRoot)
	contextService := api.NewContextService(
		repoIndex,
		contextbuild.NewBuilder(repoIndex),
		api.NewMemoryContextStore(),
	)

	server, err := api.NewServer(api.Options{
		Config: cfg,
		DB:     pool,
		PendingMigrations: func(ctx context.Context) ([]migrations.Migration, error) {
			return migrations.Pending(ctx, pool, cfg.MigrationsDir)
		},
		Services:         services,
		SubjectDirectory: api.NewSubjectDirectory(store.NewUsers(pool), store.NewOrganizations(pool)),
		Auth: api.NewAuthServices(api.AuthServicesConfig{
			Sessions:      store.NewSessions(pool),
			Identities:    store.NewIdentities(pool),
			Members:       store.NewMembers(pool),
			Users:         store.NewUsers(pool),
			Organizations: store.NewOrganizations(pool),
			Credentials:   store.NewCredentials(pool),
		}),
		Encryptor:  encryptor,
		Gateway:    gateway,
		GitHubApp:  githubApp,
		Workspaces: workspaces,
		Runs:       runsRepo,
		Queue:      taskQueue,
		Tools:      toolService,
		Context:    contextService,
		Logger:     logger,
		Version:    version,
		GitCommit:  gitCommit,
		BuildTime:  buildTime,
	})
	if err != nil {
		return err
	}
	logger.Info("services wired",
		"m01", "organizations, projects, audit, idempotency",
		"m02", "sessions, identities, members, credentials",
		"credential_key_version", credentialKeyVersion(encryptor))

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("api shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return <-errCh
	}
}

func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	if cfg.LogFormat == "text" {
		handler = slog.NewTextHandler(os.Stderr, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}
	return slog.New(handler)
}

// credentialKeyVersion reports the envelope key version, or 0 when encryption
// is disabled (migrate-only runs).
func credentialKeyVersion(encryptor *secrets.Encryptor) int {
	if encryptor == nil {
		return 0
	}
	return encryptor.KeyVersion()
}

// githubAppID reads the configured GitHub App id, or 0 when unset.
func githubAppID(cfg *config.Config) int64 {
	if strings.TrimSpace(cfg.GitHubAppID) == "" {
		return 0
	}
	id, err := strconv.ParseInt(strings.TrimSpace(cfg.GitHubAppID), 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// githubPrivateKey loads the App private key from its configured path.
func githubPrivateKey(cfg *config.Config) []byte {
	if strings.TrimSpace(cfg.GitHubAppKeyPath) == "" {
		return nil
	}
	raw, err := os.ReadFile(strings.TrimSpace(cfg.GitHubAppKeyPath))
	if err != nil {
		return nil
	}
	return raw
}

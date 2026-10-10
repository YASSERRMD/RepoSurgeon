// Command api serves the RepoSurgeon HTTP API.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/api"
	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
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

	cfg, err := config.Load()
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
	server, err := api.NewServer(api.Options{
		Config: cfg,
		DB:     pool,
		PendingMigrations: func(ctx context.Context) ([]migrations.Migration, error) {
			return migrations.Pending(ctx, pool, cfg.MigrationsDir)
		},
		Services:         services,
		SubjectDirectory: api.NewSubjectDirectory(store.NewUsers(pool), store.NewOrganizations(pool)),
		Logger:           logger,
		Version:          version,
		GitCommit:        gitCommit,
		BuildTime:        buildTime,
	})
	logger.Info("m01 services wired", "services", "organizations, projects, audit, idempotency")
	if err != nil {
		return err
	}

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

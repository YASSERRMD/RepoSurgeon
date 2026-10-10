// Command worker runs the RepoSurgeon background supervisor.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
	"github.com/YASSERRMD/RepoSurgeon/internal/worker"
)

func main() {
	if err := run(); err != nil {
		slog.Error("worker exited", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := newLogger(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := store.Open(ctx, cfg.DatabaseURL, store.DefaultOptions())
	if err != nil {
		return err
	}
	defer pool.Close()

	sup := &worker.Supervisor{
		DB: pool,
		PendingMigrations: func(ctx context.Context) ([]migrations.Migration, error) {
			return migrations.Pending(ctx, pool, cfg.MigrationsDir)
		},
		Logger: logger,
	}
	// Fail fast when the schema is behind instead of looping on stale state.
	pending, err := migrations.Pending(ctx, pool, cfg.MigrationsDir)
	if err != nil {
		return err
	}
	for _, mg := range pending {
		if mg.Critical {
			return errors.New("critical migrations pending; run `make migrate-up` before starting the worker")
		}
	}

	httpServer := &http.Server{
		Addr:              cfg.WorkerAddr,
		Handler:           worker.HealthHandler(sup),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("worker health endpoint listening", "addr", cfg.WorkerAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	go func() {
		if err := sup.Run(ctx); err != nil {
			logger.Error("supervisor stopped", "err", err)
			stop()
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		logger.Info("worker shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

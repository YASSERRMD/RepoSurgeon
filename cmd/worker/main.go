// Command worker runs the RepoSurgeon durable orchestration loop.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/agent"
	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/queue"
	"github.com/YASSERRMD/RepoSurgeon/internal/runner"
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

	pending, err := migrations.Pending(ctx, pool, cfg.MigrationsDir)
	if err != nil {
		return err
	}
	for _, mg := range pending {
		if mg.Critical {
			return errors.New("critical migrations pending; run `make migrate-up` before starting the worker")
		}
	}

	q := queue.New(pool)
	runs := store.NewRuns(pool)

	// M06 has no agent steps yet; the handler records the durable facts and
	// completes. Later milestones replace it with the real pipeline.
	handler := worker.StepHandlerFunc(func(_ context.Context, task queue.Task, heartbeat func() error) error {
		if err := heartbeat(); err != nil {
			return err
		}
		logger.Info("task executed", "task", task.ID, "kind", task.Kind, "run", task.RunID)
		return nil
	})

	workerCfg := worker.DefaultConfig()
	workerCfg.Name, _ = os.Hostname()
	if workerCfg.Name == "" {
		workerCfg.Name = "worker"
	}
	if lease := envSeconds("RS_WORKER_LEASE_SECONDS"); lease > 0 {
		workerCfg.Lease = lease
	}
	if kinds := strings.TrimSpace(os.Getenv("RS_WORKER_KINDS")); kinds != "" {
		workerCfg.Kinds = strings.Split(kinds, ",")
	}

	orch, err := worker.New(workerCfg, q, &runStoreAdapter{runs: runs}, handler, logger)
	if err != nil {
		return err
	}

	// Liveness stays independent of the loop; readiness reports the loop state.
	httpServer := &http.Server{
		Addr:              cfg.WorkerAddr,
		Handler:           worker.HealthHandler(&worker.Supervisor{Worker: orch}),
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
		if err := orch.Run(ctx); err != nil {
			logger.Error("orchestration loop stopped", "err", err)
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

// runStoreAdapter adapts the run repository to the worker's interface.
type runStoreAdapter struct {
	runs *store.Runs
}

func (a *runStoreAdapter) Get(ctx context.Context, organizationID, runID string) (worker.RunView, error) {
	run, err := a.runs.Get(ctx, organizationID, runID)
	if err != nil {
		return worker.RunView{}, err
	}
	return worker.RunView{ID: run.ID, OrganizationID: run.OrganizationID, ProjectID: run.ProjectID, State: run.State}, nil
}

func (a *runStoreAdapter) Transition(ctx context.Context, organizationID, runID, toState, reason, actorType string) (worker.RunView, error) {
	run, err := a.runs.Transition(ctx, organizationID, runID, toState, reason, actorType)
	if err != nil {
		return worker.RunView{}, err
	}
	return worker.RunView{ID: run.ID, OrganizationID: run.OrganizationID, ProjectID: run.ProjectID, State: run.State}, nil
}

func envSeconds(name string) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// agentUnused keeps the import honest until run tasks drive real steps.
var _ = agent.StateQueued
var _ = runner.NewServer

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

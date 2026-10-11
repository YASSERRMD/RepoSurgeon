// Package worker implements the durable orchestration loop: claim a task,
// drive its run through the state machine, heartbeat the lease and record the
// outcome. A worker that dies mid-task loses only its lease; another worker
// picks the task up with an incremented attempt.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/agent"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/queue"
)

// StepHandler performs the work for a claimed task. It is called with a
// heartbeat function that must be called periodically for long work.
type StepHandler interface {
	HandleRun(ctx context.Context, task queue.Task, heartbeat func() error) error
}

// StepHandlerFunc adapts a function to StepHandler.
type StepHandlerFunc func(ctx context.Context, task queue.Task, heartbeat func() error) error

// HandleRun implements StepHandler.
func (f StepHandlerFunc) HandleRun(ctx context.Context, task queue.Task, heartbeat func() error) error {
	return f(ctx, task, heartbeat)
}

// Config configures one worker instance.
type Config struct {
	Name         string
	Lease        time.Duration
	PollInterval time.Duration
	Kinds        []string
}

// DefaultConfig returns conservative worker defaults.
func DefaultConfig() Config {
	return Config{
		Name:         "worker",
		Lease:        60 * time.Second,
		PollInterval: 2 * time.Second,
	}
}

// TaskQueue is the queue surface the worker needs.
type TaskQueue interface {
	Claim(ctx context.Context, owner string, lease time.Duration) (queue.Task, error)
	ClaimKind(ctx context.Context, owner, kind string, lease time.Duration) (queue.Task, error)
	Heartbeat(ctx context.Context, owner, taskID string, lease time.Duration) error
	Complete(ctx context.Context, owner, taskID string) error
	Fail(ctx context.Context, owner, taskID, reason string, retryDelay time.Duration) error
}

// Worker claims tasks and drives them through the state machine.
type Worker struct {
	cfg     Config
	queue   TaskQueue
	runs    RunStore
	handler StepHandler
	log     *slog.Logger
	now     func() time.Time
}

// RunStore is the run repository surface the worker needs.
type RunStore interface {
	Get(ctx context.Context, organizationID, runID string) (RunView, error)
	Transition(ctx context.Context, organizationID, runID, toState, reason, actorType string) (RunView, error)
}

// RunView is the run data the worker uses.
type RunView struct {
	ID             string
	OrganizationID string
	ProjectID      string
	State          string
}

// New builds a worker.
func New(cfg Config, q TaskQueue, runs RunStore, handler StepHandler, log *slog.Logger) (*Worker, error) {
	if q == nil {
		return nil, errors.New("worker: queue is required")
	}
	if runs == nil {
		return nil, errors.New("worker: run store is required")
	}
	if handler == nil {
		return nil, errors.New("worker: step handler is required")
	}
	if log == nil {
		log = slog.Default()
	}
	if cfg.Lease <= 0 {
		cfg.Lease = time.Minute
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = 2 * time.Second
	}
	if cfg.Name == "" {
		cfg.Name = "worker"
	}
	return &Worker{cfg: cfg, queue: q, runs: runs, handler: handler, log: log, now: time.Now}, nil
}

// Run executes supervision cycles until ctx is canceled.
func (w *Worker) Run(ctx context.Context) error {
	w.log.Info("worker started", "name", w.cfg.Name, "lease", w.cfg.Lease, "kinds", w.cfg.Kinds)
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := w.RunOnce(ctx); err != nil {
			var noTask *queue.NoTaskError
			if errors.As(err, &noTask) {
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(w.cfg.PollInterval):
				}
				continue
			}
			// Unexpected failures are logged; the loop keeps serving other tasks.
			w.log.Error("worker cycle failed", "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(w.cfg.PollInterval):
			}
		}
	}
}

// RunOnce performs a single claim-and-drive cycle. It returns a NoTaskError when
// there was nothing to claim.
func (w *Worker) RunOnce(ctx context.Context) error {
	task, err := w.claim(ctx)
	if err != nil {
		return err
	}
	return w.execute(ctx, task)
}

func (w *Worker) claim(ctx context.Context) (queue.Task, error) {
	if len(w.cfg.Kinds) == 0 {
		task, err := w.queue.Claim(ctx, w.cfg.Name, w.cfg.Lease)
		return task, classify(err)
	}
	var lastErr error
	for _, kind := range w.cfg.Kinds {
		task, err := w.queue.ClaimKind(ctx, w.cfg.Name, kind, w.cfg.Lease)
		if err == nil {
			return task, nil
		}
		if !errors.Is(err, domain.ErrNoTaskAvailable) {
			lastErr = err
		}
	}
	if lastErr != nil {
		return queue.Task{}, lastErr
	}
	return queue.Task{}, &NoTaskError{}
}

// classify converts an empty claim into NoTaskError for every implementation.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, domain.ErrNoTaskAvailable) {
		return &NoTaskError{}
	}
	return err
}

// pipeline returns the durable states a run walks through, in order. The
// handler performs the real work for each state in later milestones; M06
// drives the machine so the transitions and history are exercised for real.
func pipeline() []string {
	return []string{
		agent.StateIntaking, agent.StateAnalyzing, agent.StatePlanning,
		agent.StateEditing, agent.StateTesting, agent.StateReviewing,
		agent.StatePRReady, agent.StatePROpen, agent.StateCompleted,
	}
}

// execute drives one claimed task. A task may be reclaimed after a crash; the
// attempt counter makes that visible and the state machine refuses illegal
// re-entry.
func (w *Worker) execute(ctx context.Context, task queue.Task) error {
	w.log.Info("task claimed", "task", task.ID, "kind", task.Kind, "attempt", task.Attempt)

	// Only run tasks drive a run state machine; other kinds are executed
	// directly so sync/probe work stays usable before runs exist.
	if task.Kind != queue.KindRun || task.RunID == "" {
		if err := w.handler.HandleRun(ctx, task, func() error { return nil }); err != nil {
			return w.fail(ctx, task, err)
		}
		return w.queue.Complete(ctx, w.cfg.Name, task.ID)
	}

	if _, err := w.runs.Get(ctx, task.OrganizationID, task.RunID); err != nil {
		if errors.Is(err, agent.ErrRunNotFound) {
			// The run was deleted; the task has nothing left to do.
			return w.queue.Complete(ctx, w.cfg.Name, task.ID)
		}
		return w.fail(ctx, task, err)
	}

	heartbeat := func() error {
		return w.queue.Heartbeat(ctx, w.cfg.Name, task.ID, w.cfg.Lease)
	}

	// Walk the durable pipeline, tolerating a run that already progressed (for
	// example after a crash mid-transition).
	for _, state := range pipeline() {
		current, err := w.runs.Get(ctx, task.OrganizationID, task.RunID)
		if err != nil {
			return w.fail(ctx, task, err)
		}
		if current.State == state {
			continue
		}
		if !agent.CanTransition(current.State, state) {
			// The run is already past this state, or waiting elsewhere.
			continue
		}
		if _, err := w.runs.Transition(ctx, task.OrganizationID, task.RunID, state, "worker advanced run", "worker"); err != nil {
			return w.fail(ctx, task, err)
		}
		if err := w.handler.HandleRun(ctx, task, heartbeat); err != nil {
			return w.fail(ctx, task, err)
		}
	}
	return w.queue.Complete(ctx, w.cfg.Name, task.ID)
}

// fail records the failure and decides whether the task retries.
func (w *Worker) fail(ctx context.Context, task queue.Task, cause error) error {
	w.log.Error("task failed", "task", task.ID, "attempt", task.Attempt, "err", cause.Error())
	if task.Kind == queue.KindRun && task.RunID != "" {
		if _, err := w.runs.Transition(ctx, task.OrganizationID, task.RunID, agent.StateFailed, cause.Error(), "worker"); err != nil && !errors.Is(err, agent.ErrRunNotFound) {
			// A transition failure must not hide the original cause.
			w.log.Error("failed to mark run failed", "run", task.RunID, "err", err)
		}
	}
	retryDelay := time.Duration(task.Attempt) * time.Second
	return w.queue.Fail(ctx, w.cfg.Name, task.ID, cause.Error(), retryDelay)
}

// NoTaskError signals that a claim found nothing to work on.
type NoTaskError struct{}

func (e *NoTaskError) Error() string { return "worker: no task available" }

package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/agent"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/queue"
)

// fakeQueue is an in-memory task queue for worker tests.
type fakeQueue struct {
	tasks      []queue.Task
	claims     int
	heartbeats int
	completes  int
	fails      int
}

func (f *fakeQueue) Claim(_ context.Context, owner string, lease time.Duration) (queue.Task, error) {
	return f.ClaimKind(context.Background(), owner, "", lease)
}

func (f *fakeQueue) ClaimKind(_ context.Context, owner, _ string, _ time.Duration) (queue.Task, error) {
	for i, task := range f.tasks {
		if task.Status == queue.StatusPending {
			f.tasks[i].Status = queue.StatusRunning
			f.tasks[i].Attempt++
			f.tasks[i].LeaseOwner = owner
			f.claims++
			return f.tasks[i], nil
		}
	}
	return queue.Task{}, domain.ErrNoTaskAvailable
}

func (f *fakeQueue) Heartbeat(context.Context, string, string, time.Duration) error {
	f.heartbeats++
	return nil
}

func (f *fakeQueue) Complete(_ context.Context, _, taskID string) error {
	for i, task := range f.tasks {
		if task.ID == taskID {
			f.tasks[i].Status = queue.StatusCompleted
			f.completes++
		}
	}
	return nil
}

func (f *fakeQueue) Fail(_ context.Context, _, taskID, _ string, _ time.Duration) error {
	for i, task := range f.tasks {
		if task.ID == taskID {
			f.tasks[i].Status = queue.StatusFailed
			f.fails++
		}
	}
	return nil
}

// fakeRuns is an in-memory run store.
type fakeRuns struct {
	runs   map[string]*RunView
	states []string
}

func newFakeRuns() *fakeRuns {
	return &fakeRuns{runs: map[string]*RunView{}}
}

func (f *fakeRuns) Get(_ context.Context, _, runID string) (RunView, error) {
	run, ok := f.runs[runID]
	if !ok {
		return RunView{}, agent.ErrRunNotFound
	}
	return *run, nil
}

func (f *fakeRuns) Transition(_ context.Context, _, runID, toState, _, _ string) (RunView, error) {
	run, ok := f.runs[runID]
	if !ok {
		return RunView{}, agent.ErrRunNotFound
	}
	if err := agent.Transition(run.State, toState); err != nil {
		return RunView{}, err
	}
	run.State = toState
	f.states = append(f.states, toState)
	return *run, nil
}

func TestWorkerDrivesRunToCompletion(t *testing.T) {
	q := &fakeQueue{tasks: []queue.Task{{ID: "t1", OrganizationID: "org_1", ProjectID: "prj_1", Kind: queue.KindRun, RunID: "run_1", Status: queue.StatusPending}}}
	runs := newFakeRuns()
	runs.runs["run_1"] = &RunView{ID: "run_1", OrganizationID: "org_1", ProjectID: "prj_1", State: agent.StateQueued}

	handled := 0
	handler := StepHandlerFunc(func(_ context.Context, _ queue.Task, heartbeat func() error) error {
		handled++
		return heartbeat()
	})
	w, err := New(DefaultConfig(), q, runs, handler, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// The handler runs once per durable pipeline state.
	if handled != len(pipeline()) {
		t.Fatalf("handler calls = %d, want %d", handled, len(pipeline()))
	}
	if q.completes != 1 {
		t.Fatalf("completes = %d", q.completes)
	}
	if runs.runs["run_1"].State != agent.StateCompleted {
		t.Fatalf("run state = %q", runs.runs["run_1"].State)
	}
	// The run advanced through intaking before the handler ran.
	if len(runs.states) < 2 || runs.states[0] != agent.StateIntaking {
		t.Fatalf("states = %v", runs.states)
	}
}

func TestWorkerMarksRunFailedOnHandlerError(t *testing.T) {
	q := &fakeQueue{tasks: []queue.Task{{ID: "t1", OrganizationID: "org_1", Kind: queue.KindRun, RunID: "run_1", Status: queue.StatusPending}}}
	runs := newFakeRuns()
	runs.runs["run_1"] = &RunView{ID: "run_1", OrganizationID: "org_1", State: agent.StateQueued}

	handler := StepHandlerFunc(func(context.Context, queue.Task, func() error) error {
		return errors.New("handler exploded")
	})
	w, _ := New(DefaultConfig(), q, runs, handler, nil)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if runs.runs["run_1"].State != agent.StateFailed {
		t.Fatalf("run state = %q, want %q", runs.runs["run_1"].State, agent.StateFailed)
	}
	if q.fails != 1 {
		t.Fatalf("fails = %d", q.fails)
	}
	if q.completes != 0 {
		t.Fatal("a failed task must not complete")
	}
}

func TestWorkerHandlesNonRunTasks(t *testing.T) {
	q := &fakeQueue{tasks: []queue.Task{{ID: "t1", OrganizationID: "org_1", Kind: queue.KindProbe, Status: queue.StatusPending}}}
	runs := newFakeRuns()
	handled := 0
	handler := StepHandlerFunc(func(context.Context, queue.Task, func() error) error {
		handled++
		return nil
	})
	w, _ := New(DefaultConfig(), q, runs, handler, nil)
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if handled != 1 || q.completes != 1 {
		t.Fatalf("handled = %d completes = %d", handled, q.completes)
	}
}

func TestWorkerReportsNoTask(t *testing.T) {
	q := &fakeQueue{}
	runs := newFakeRuns()
	handler := StepHandlerFunc(func(context.Context, queue.Task, func() error) error { return nil })
	w, _ := New(DefaultConfig(), q, runs, handler, nil)
	err := w.RunOnce(context.Background())
	var noTask *NoTaskError
	if !errors.As(err, &noTask) {
		t.Fatalf("err = %v, want NoTaskError", err)
	}
}

func TestWorkerRunStopsOnCancel(t *testing.T) {
	q := &fakeQueue{}
	runs := newFakeRuns()
	handler := StepHandlerFunc(func(context.Context, queue.Task, func() error) error { return nil })
	cfg := DefaultConfig()
	cfg.PollInterval = 5 * time.Millisecond
	w, _ := New(cfg, q, runs, handler, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := w.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestNewValidatesDependencies(t *testing.T) {
	handler := StepHandlerFunc(func(context.Context, queue.Task, func() error) error { return nil })
	if _, err := New(DefaultConfig(), nil, newFakeRuns(), handler, nil); err == nil {
		t.Fatal("missing queue must be rejected")
	}
	if _, err := New(DefaultConfig(), &fakeQueue{}, nil, handler, nil); err == nil {
		t.Fatal("missing run store must be rejected")
	}
	if _, err := New(DefaultConfig(), &fakeQueue{}, newFakeRuns(), nil, nil); err == nil {
		t.Fatal("missing handler must be rejected")
	}
}

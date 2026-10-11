package api

import (
	"context"

	"github.com/YASSERRMD/RepoSurgeon/internal/queue"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// RunScheduler is the run repository surface the API uses. It is an interface
// so handler tests can substitute a fake.
type RunScheduler interface {
	Create(ctx context.Context, run store.Run) (store.Run, error)
	Get(ctx context.Context, organizationID, runID string) (store.Run, error)
	Transition(ctx context.Context, organizationID, runID, toState, reason, actorType string) (store.Run, error)
	ListForProject(ctx context.Context, projectID string, limit int, cursor string) (store.Page[store.Run], error)
	History(ctx context.Context, runID string) ([]store.StateChange, error)
}

// TaskQueueService is the queue surface the API uses.
type TaskQueueService interface {
	Enqueue(ctx context.Context, task queue.Task) (queue.Task, error)
}

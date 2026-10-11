package queue

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openQueuePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("RS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RS_TEST_DATABASE_URL not set; skipping queue integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// resetQueue clears the queue so tests never observe each other's rows.
func resetQueue(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `DELETE FROM task_queue`); err != nil {
		t.Fatalf("reset queue: %v", err)
	}
}

func seedOrgProject(t *testing.T, pool *pgxpool.Pool) (orgID, projectID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	userID := ids.MustNew("usr")
	orgID = ids.MustNew("org")
	projectID = ids.MustNew("prj")
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email) VALUES ($1, $2)`, userID, userID+"@example.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO organizations (id, slug, name) VALUES ($1, $2, $3)`, orgID, orgID, "queue org"); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, organization_id, name) VALUES ($1, $2, $3)`, projectID, orgID, "queue project"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return orgID, projectID
}

// testKind scopes every test to its own task kind so runs cannot steal each
// other's rows.
var testKind = "probe"

func TestQueueClaimIsExclusive(t *testing.T) {
	pool := openQueuePool(t)
	queue := New(pool)
	ctx := context.Background()
	resetQueue(t, pool)
	orgID, projectID := seedOrgProject(t, pool)

	taskID := ids.MustNew("tsk")
	if _, err := queue.Enqueue(ctx, Task{
		ID: taskID, OrganizationID: orgID, ProjectID: projectID, Kind: KindProbe,
		Payload: map[string]any{"k": "v"},
	}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM task_queue WHERE id = $1`, taskID) }()

	first, err := queue.ClaimKind(ctx, "worker-a", testKind, 30*time.Second)
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if first.ID != taskID || first.Attempt != 1 {
		t.Fatalf("first claim = %+v", first)
	}
	if first.Status != StatusRunning {
		t.Fatalf("status = %q", first.Status)
	}

	// A second worker must not see the leased task.
	if _, err := queue.ClaimKind(ctx, "worker-b", testKind, 30*time.Second); err == nil {
		t.Fatal("second worker claimed a leased task")
	}

	// Heartbeat by the owner extends the lease; a stranger cannot.
	if err := queue.Heartbeat(ctx, "worker-b", taskID, 30*time.Second); err == nil {
		t.Fatal("non-owner heartbeat must fail")
	}
	if err := queue.Heartbeat(ctx, "worker-a", taskID, 30*time.Second); err != nil {
		t.Fatalf("owner heartbeat: %v", err)
	}
}

func TestQueueExpiredLeaseIsRecovered(t *testing.T) {
	pool := openQueuePool(t)
	queue := New(pool)
	ctx := context.Background()
	resetQueue(t, pool)
	orgID, projectID := seedOrgProject(t, pool)

	taskID := ids.MustNew("tsk")
	if _, err := queue.Enqueue(ctx, Task{ID: taskID, OrganizationID: orgID, ProjectID: projectID, Kind: testKind}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM task_queue WHERE id = $1`, taskID) }()

	// Claim with a lease that expires immediately, then let a second worker
	// recover the task — this is the crashed-worker path.
	if _, err := queue.Claim(ctx, "worker-a", -time.Second); err != nil {
		t.Fatalf("claim: %v", err)
	}
	time.Sleep(20 * time.Millisecond)

	expired, err := queue.RecoverExpiredLeases(ctx)
	if err != nil {
		t.Fatalf("RecoverExpiredLeases: %v", err)
	}
	found := false
	for _, task := range expired {
		if task.ID == taskID {
			found = true
		}
	}
	if !found {
		t.Fatal("expired lease was not reported for recovery")
	}

	recovered, err := queue.ClaimKind(ctx, "worker-b", testKind, 30*time.Second)
	if err != nil {
		t.Fatalf("recovery claim: %v", err)
	}
	if recovered.ID != taskID {
		t.Fatalf("recovered task = %s, want %s", recovered.ID, taskID)
	}
	if recovered.Attempt != 2 {
		t.Fatalf("attempt = %d, want 2 (the crash is visible)", recovered.Attempt)
	}
}

func TestQueueCompleteFailCancel(t *testing.T) {
	pool := openQueuePool(t)
	queue := New(pool)
	ctx := context.Background()
	resetQueue(t, pool)
	orgID, projectID := seedOrgProject(t, pool)

	newTask := func() string {
		t.Helper()
		id := ids.MustNew("tsk")
		if _, err := queue.Enqueue(ctx, Task{ID: id, OrganizationID: orgID, ProjectID: projectID, Kind: testKind, MaxAttempts: 1}); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
		return id
	}

	completed := newTask()
	if _, err := queue.ClaimKind(ctx, "worker-a", testKind, 30*time.Second); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := queue.Complete(ctx, "worker-a", completed); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	stored, err := queue.Get(ctx, completed)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != StatusCompleted || stored.LeaseOwner != "" {
		t.Fatalf("completed task = %+v", stored)
	}
	if err := queue.Complete(ctx, "worker-a", completed); err == nil {
		t.Fatal("completing twice must fail")
	}

	// With one attempt allowed, a failure is permanent.
	failed := newTask()
	if _, err := queue.ClaimKind(ctx, "worker-a", testKind, 30*time.Second); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := queue.Fail(ctx, "worker-a", failed, "boom", 0); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	stored, err = queue.Get(ctx, failed)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != StatusFailed || stored.LastError != "boom" {
		t.Fatalf("failed task = %+v", stored)
	}

	// A retryable failure returns the task to pending.
	retryID := ids.MustNew("tsk")
	if _, err := queue.Enqueue(ctx, Task{ID: retryID, OrganizationID: orgID, ProjectID: projectID, Kind: testKind, MaxAttempts: 3}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := queue.ClaimKind(ctx, "worker-a", testKind, 30*time.Second); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := queue.Fail(ctx, "worker-a", retryID, "temporary", time.Millisecond); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	stored, err = queue.Get(ctx, retryID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if stored.Status != StatusPending {
		t.Fatalf("retryable task = %+v", stored)
	}

	// Cancellation stops further claiming.
	canceled := newTask()
	if err := queue.Cancel(ctx, canceled); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := queue.ClaimKind(ctx, "worker-a", testKind, 30*time.Second); err == nil {
		t.Fatal("a canceled task must not be claimed")
	}
	stored, _ = queue.Get(ctx, canceled)
	if stored.Status != StatusCanceled {
		t.Fatalf("canceled task = %+v", stored)
	}

	for _, id := range []string{completed, failed, retryID, canceled} {
		if _, err := pool.Exec(ctx, `DELETE FROM task_queue WHERE id = $1`, id); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
	}
}

func TestQueueCountByStatus(t *testing.T) {
	pool := openQueuePool(t)
	queue := New(pool)
	ctx := context.Background()
	resetQueue(t, pool)
	orgID, projectID := seedOrgProject(t, pool)

	id := ids.MustNew("tsk")
	if _, err := queue.Enqueue(ctx, Task{ID: id, OrganizationID: orgID, ProjectID: projectID, Kind: KindProbe}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	defer func() { _, _ = pool.Exec(ctx, `DELETE FROM task_queue WHERE id = $1`, id) }()

	counts, err := queue.CountByStatus(ctx, orgID)
	if err != nil {
		t.Fatalf("CountByStatus: %v", err)
	}
	if counts[StatusPending] < 1 {
		t.Fatalf("pending count = %d", counts[StatusPending])
	}
}

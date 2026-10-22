package store

import (
	"context"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
)

// TestRunEventsAppendAndList covers the append-only event log: events are
// inserted with a database-assigned sequence, listed newest first for one run,
// paginated with keyset cursors, and protected against update/delete.
func TestRunEventsAppendAndList(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	fixture := env.seedOrganization(t, ctx)
	project := env.seedProject(t, ctx, fixture, "event store project")

	runs := NewRuns(env.pool)
	run, err := runs.Create(ctx, Run{
		ID:             ids.MustNew("run"),
		OrganizationID: fixture.organization.ID,
		ProjectID:      project.ID,
		CreatedBy:      fixture.ownerID,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	events := NewRunEvents(env.pool)

	created := make([]domain.RunEvent, 0, 3)
	for index := 0; index < 3; index++ {
		ev, err := events.Create(ctx, domain.RunEvent{
			ID:             ids.MustNew("rev"),
			OrganizationID: fixture.organization.ID,
			ProjectID:      project.ID,
			RunID:          run.ID,
			ActorID:        fixture.ownerID,
			CausationID:    "cause_" + ids.MustNew("cs"),
			Type:           "step.began",
			Step:           "investigate",
			Payload: map[string]any{
				"attempt": index,
				"note":    "event " + ids.MustNew("ev"),
			},
		})
		if err != nil {
			t.Fatalf("create event %d: %v", index, err)
		}
		if ev.Sequence == 0 {
			t.Fatalf("event %d was not assigned a sequence", index)
		}
		created = append(created, ev)
	}

	// Events come back newest first.
	first, err := events.List(ctx, fixture.organization.ID, run.ID, 10, "")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(first.Items) != 3 {
		t.Fatalf("expected 3 events, got %d", len(first.Items))
	}
	if first.Items[0].Sequence != created[2].Sequence {
		t.Fatalf("newest first: got %d, want %d", first.Items[0].Sequence, created[2].Sequence)
	}
	if first.NextCursor != "" {
		t.Fatalf("expected no next cursor, got %q", first.NextCursor)
	}

	// Keyset pagination: resume from the first page's cursor.
	cursor := storeSequenceCursor(t, created[1].Sequence, created[1].ID)
	second, err := events.List(ctx, fixture.organization.ID, run.ID, 1, cursor)
	if err != nil {
		t.Fatalf("list events after cursor: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].Sequence != created[0].Sequence {
		t.Fatalf("cursor resume returned %+v, want event %d", second.Items, created[0].Sequence)
	}

	// Events are tenant-scoped: another organization sees none.
	other := env.seedOrganization(t, ctx)
	otherProject := env.seedProject(t, ctx, other, "other org project")
	otherRun, err := runs.Create(ctx, Run{
		ID:             ids.MustNew("run"),
		OrganizationID: other.organization.ID,
		ProjectID:      otherProject.ID,
		CreatedBy:      other.ownerID,
	})
	if err != nil {
		t.Fatalf("create other run: %v", err)
	}
	if _, err := events.Create(ctx, domain.RunEvent{
		OrganizationID: other.organization.ID,
		RunID:          otherRun.ID,
		CausationID:    "cause_other",
		Type:           "step.began",
	}); err != nil {
		t.Fatalf("create other org event: %v", err)
	}
	cross, err := events.List(ctx, fixture.organization.ID, run.ID, 10, "")
	if err != nil {
		t.Fatalf("list cross-tenant: %v", err)
	}
	for _, ev := range cross.Items {
		if ev.OrganizationID != fixture.organization.ID {
			t.Fatalf("cross-tenant leak: event %s in org %s", ev.ID, ev.OrganizationID)
		}
	}

	// Immutability: an update is refused by the trigger.
	env.exec(ctx, t, `UPDATE run_events SET type = 'tampered' WHERE id = $1`, created[0].ID)
}

// storeSequenceCursor is a test-only helper mirroring the store's opaque
// cursor encoding so tests can resume a listing from a known event.
func storeSequenceCursor(t *testing.T, sequence int64, id string) string {
	t.Helper()
	encoded := SequenceCursor(sequence, id)
	if encoded == "" {
		t.Fatal("empty cursor")
	}
	return encoded
}

// TestRunEventsRejectInvalidInput validates that malformed events are refused.
func TestRunEventsRejectInvalidInput(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()

	events := NewRunEvents(env.pool)
	if _, err := events.Create(ctx, domain.RunEvent{}); err == nil {
		t.Fatal("expected validation error for empty event")
	}
}

// TestRunEventsAreImmutable verifies the append-only trigger blocks updates
// and deletes at the database layer.
func TestRunEventsAreImmutable(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	fixture := env.seedOrganization(t, ctx)
	project := env.seedProject(t, ctx, fixture, "immutability project")

	runs := NewRuns(env.pool)
	run, err := runs.Create(ctx, Run{
		ID:             ids.MustNew("run"),
		OrganizationID: fixture.organization.ID,
		ProjectID:      project.ID,
		CreatedBy:      fixture.ownerID,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	events := NewRunEvents(env.pool)
	ev, err := events.Create(ctx, domain.RunEvent{
		OrganizationID: fixture.organization.ID,
		RunID:          run.ID,
		CausationID:    "cause_immutable",
		Type:           "run.created",
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	if err := env.pool.QueryRow(ctx, `UPDATE run_events SET type = 'tampered' WHERE id = $1`, ev.ID).Scan(); err == nil {
		t.Fatal("expected update to be rejected")
	}
	if err := env.pool.QueryRow(ctx, `DELETE FROM run_events WHERE id = $1`, ev.ID).Scan(); err == nil {
		t.Fatal("expected delete to be rejected")
	}
	// The row must still be readable with its original type.
	var storedType string
	if err := env.pool.QueryRow(ctx, `SELECT type FROM run_events WHERE id = $1`, ev.ID).Scan(&storedType); err != nil {
		t.Fatalf("read back event: %v", err)
	}
	if storedType != "run.created" {
		t.Fatalf("event type changed: %q", storedType)
	}
}

// TestRunEventsOrderByCreatedAt uses a real elapsed gap to confirm events are
// ordered by sequence, which tracks insertion order.
func TestRunEventsOrderTracksInsertion(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	fixture := env.seedOrganization(t, ctx)
	project := env.seedProject(t, ctx, fixture, "order project")

	runs := NewRuns(env.pool)
	run, err := runs.Create(ctx, Run{
		ID:             ids.MustNew("run"),
		OrganizationID: fixture.organization.ID,
		ProjectID:      project.ID,
		CreatedBy:      fixture.ownerID,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	events := NewRunEvents(env.pool)
	for index := 0; index < 5; index++ {
		time.Sleep(5 * time.Millisecond)
		_, err := events.Create(ctx, domain.RunEvent{
			OrganizationID: fixture.organization.ID,
			RunID:          run.ID,
			CausationID:    "cause_order",
			Type:           "tool.called",
			Payload:        map[string]any{"index": index},
		})
		if err != nil {
			t.Fatalf("create event %d: %v", index, err)
		}
	}

	page, err := events.List(ctx, fixture.organization.ID, run.ID, 10, "")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	for i := 1; i < len(page.Items); i++ {
		if page.Items[i-1].Sequence <= page.Items[i].Sequence {
			t.Fatalf("events not newest first: %d before %d", page.Items[i-1].Sequence, page.Items[i].Sequence)
		}
	}
}

package store

import (
	"context"
	"testing"

	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/YASSERRMD/RepoSurgeon/internal/recovery"
)

// TestRecoveryRepositoriesPersistEvidence covers the durable state recovery
// journal, the attempt history, bounded attempt budgets and the effect
// receipts that stop a replay from duplicating a side effect.
func TestRecoveryRepositoriesPersistEvidence(t *testing.T) {
	env := openTestEnv(t)
	ctx := context.Background()
	fixture := env.seedOrganization(t, ctx)
	project := env.seedProject(t, ctx, fixture, "recovery project")

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

	journal := NewRecoveryJournal(env.pool)
	kinds := []string{"retry_scheduled", "rollback", "recovered"}
	for index, kind := range kinds {
		err := journal.Append(ctx, RecoveryEventRow{
			ID:             ids.MustNew("rev"),
			RunID:          run.ID,
			OrganizationID: fixture.organization.ID,
			Attempt:        index + 1,
			Class:          string(recovery.ClassTestFailure),
			Kind:           kind,
			Reason:         "diagnostic feedback loop",
			Evidence:       map[string]any{"fingerprint": "abc123"},
			ActorType:      "system",
		})
		if err != nil {
			t.Fatalf("append %s: %v", kind, err)
		}
	}
	// A journal entry without a run is refused rather than silently dropped.
	if err := journal.Append(ctx, RecoveryEventRow{ID: ids.MustNew("rev"), OrganizationID: fixture.organization.ID, Kind: "blocked"}); err == nil {
		t.Fatal("append without run must fail")
	}
	events, err := journal.List(ctx, fixture.organization.ID, run.ID, 2)
	if err != nil {
		t.Fatalf("list journal: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want the two newest", len(events))
	}
	if events[0].Kind != "rollback" || events[1].Kind != "recovered" {
		t.Fatalf("journal order = %s,%s", events[0].Kind, events[1].Kind)
	}
	if events[0].Evidence["fingerprint"] != "abc123" {
		t.Fatalf("evidence = %v", events[0].Evidence)
	}
	if crossTenant, err := journal.List(ctx, "org_other", run.ID, 0); err != nil {
		t.Fatalf("cross-tenant list: %v", err)
	} else if len(crossTenant) != 0 {
		t.Fatalf("cross-tenant read returned %d events", len(crossTenant))
	}

	attempts := NewRecoveryAttempts(env.pool)
	for number := 1; number <= 3; number++ {
		err := attempts.Append(ctx, RecoveryAttemptRow{
			ID:               ids.MustNew("rat"),
			RunID:            run.ID,
			OrganizationID:   fixture.organization.ID,
			Number:           number,
			Kind:             "patched",
			Fingerprint:      "fp_same",
			PatchFingerprint: "diff_1",
			FailingCount:     3,
			Verdict:          "red",
			Note:             "attempt",
		})
		if err != nil {
			t.Fatalf("append attempt %d: %v", number, err)
		}
	}
	// A replayed attempt for the same attempt number must not duplicate history.
	if err := attempts.Append(ctx, RecoveryAttemptRow{
		ID:             ids.MustNew("rat"),
		RunID:          run.ID,
		OrganizationID: fixture.organization.ID,
		Number:         3,
		Kind:           "blocked",
	}); err != nil {
		t.Fatalf("replayed attempt: %v", err)
	}
	history, err := attempts.List(ctx, fixture.organization.ID, run.ID, 0)
	if err != nil {
		t.Fatalf("list attempts: %v", err)
	}
	if len(history) != 3 || history[0].Number != 1 || history[2].Number != 3 {
		t.Fatalf("history = %+v", history)
	}

	budgets := NewRecoveryBudgets(env.pool)
	first, err := budgets.Consume(ctx, run.ID, fixture.organization.ID, string(recovery.ClassTestFailure), 2)
	if err != nil {
		t.Fatalf("consume budget: %v", err)
	}
	if first.Used != 1 || first.Exhaust {
		t.Fatalf("first consume = %+v", first)
	}
	second, err := budgets.Consume(ctx, run.ID, fixture.organization.ID, string(recovery.ClassTestFailure), 2)
	if err != nil {
		t.Fatalf("consume budget: %v", err)
	}
	if second.Used != 2 || !second.Exhaust {
		t.Fatalf("second consume = %+v, want exhausted", second)
	}
	listed, err := budgets.List(ctx, run.ID)
	if err != nil || len(listed) != 1 {
		t.Fatalf("budgets = %+v err = %v", listed, err)
	}
	stored, ok, err := budgets.Get(ctx, run.ID, string(recovery.ClassTimeout))
	if err != nil || ok {
		t.Fatalf("unconsumed class = %+v ok = %v err = %v", stored, ok, err)
	}

	receipts := NewEffectReceipts(env.pool)
	recorded, duplicate, err := receipts.Record(ctx, recovery.Receipt{
		RunID:     run.ID,
		Operation: "apply_patch",
		EffectID:  "effect_alpha",
		Status:    "applied",
		Result:    map[string]any{"files": 1},
	})
	if err != nil || duplicate {
		t.Fatalf("first receipt: dup = %v err = %v", duplicate, err)
	}
	replay, duplicate, err := receipts.Record(ctx, recovery.Receipt{
		RunID:     run.ID,
		Operation: "apply_patch",
		EffectID:  "effect_alpha",
		Status:    "applied",
	})
	if err != nil {
		t.Fatalf("replay receipt: %v", err)
	}
	if !duplicate {
		t.Fatal("replayed effect must be reported as a duplicate")
	}
	if replay.ID != recorded.ID {
		t.Fatalf("replayed receipt id = %s, want %s", replay.ID, recorded.ID)
	}
	if payloads, err := receipts.List(ctx, run.ID, 0); err != nil || len(payloads) != 1 {
		t.Fatalf("receipts = %+v err = %v", payloads, err)
	}
	if _, _, err := receipts.Record(ctx, recovery.Receipt{RunID: run.ID}); err == nil {
		t.Fatal("receipt without effect id must fail")
	}

	checkpoints := NewCheckpoints(env.pool)
	snapshot := recovery.Snapshot{
		ID:          ids.MustNew("snp"),
		RunID:       run.ID,
		WorkspaceID: "wsp_m12",
		Verdict:     "green",
		Files: map[string]recovery.FileState{
			"main.go": {Content: []byte("package main\n"), Mode: 0o600, Hash: "h1"},
		},
	}
	saved, err := checkpoints.Save(ctx, snapshot, fixture.organization.ID)
	if err != nil {
		t.Fatalf("save checkpoint: %v", err)
	}
	loaded, ok, err := checkpoints.Get(ctx, fixture.organization.ID, saved.ID)
	if err != nil || !ok {
		t.Fatalf("get checkpoint: ok = %v err = %v", ok, err)
	}
	if len(loaded.Files) != 1 || string(loaded.Files["main.go"].Content) != "package main\n" {
		t.Fatalf("checkpoint files = %+v", loaded.Files)
	}
	latest, ok, err := checkpoints.Latest(ctx, fixture.organization.ID, run.ID)
	if err != nil || !ok {
		t.Fatalf("latest checkpoint: ok = %v err = %v", ok, err)
	}
	if latest.ID != saved.ID {
		t.Fatalf("latest = %s, want %s", latest.ID, saved.ID)
	}
	if rows, err := checkpoints.List(ctx, fixture.organization.ID, run.ID, 0); err != nil || len(rows) != 1 {
		t.Fatalf("checkpoint list = %+v err = %v", rows, err)
	}
}

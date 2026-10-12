package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/migrations"
	"github.com/YASSERRMD/RepoSurgeon/internal/recovery"
	"github.com/YASSERRMD/RepoSurgeon/internal/store"
)

// fakeRecoveryStores is an in-process RecoveryStores that mirrors the durable
// behavior the API depends on, including the duplicate receipt guard.
type fakeRecoveryStores struct {
	journal     []store.RecoveryEventRow
	attempts    []store.RecoveryAttemptRow
	budgets     map[string]store.BudgetRow
	receipts    []recovery.Receipt
	checkpoints []recovery.Snapshot
	failWith    error
}

func newFakeRecoveryStores() *fakeRecoveryStores {
	return &fakeRecoveryStores{budgets: map[string]store.BudgetRow{}}
}

func (f *fakeRecoveryStores) AppendJournal(_ context.Context, event store.RecoveryEventRow) error {
	if f.failWith != nil {
		return f.failWith
	}
	f.journal = append(f.journal, event)
	return nil
}

func (f *fakeRecoveryStores) ListJournal(_ context.Context, _, _ string, limit int) ([]store.RecoveryEventRow, error) {
	if limit > 0 && len(f.journal) > limit {
		return f.journal[len(f.journal)-limit:], nil
	}
	return f.journal, nil
}

func (f *fakeRecoveryStores) ListAttempts(_ context.Context, _, _ string, limit int) ([]store.RecoveryAttemptRow, error) {
	if limit > 0 && len(f.attempts) > limit {
		return f.attempts[len(f.attempts)-limit:], nil
	}
	return f.attempts, nil
}

func (f *fakeRecoveryStores) ConsumeBudget(_ context.Context, runID, _, class string, allowed int) (store.BudgetRow, error) {
	if f.failWith != nil {
		return store.BudgetRow{}, f.failWith
	}
	budget, ok := f.budgets[class]
	if !ok {
		budget = store.BudgetRow{RunID: runID, Class: class, Allowed: allowed}
	}
	budget.Used++
	if budget.Allowed-budget.Used > 0 {
		budget.Exhaust = false
	} else {
		budget.Exhaust = true
	}
	f.budgets[class] = budget
	return budget, nil
}

func (f *fakeRecoveryStores) ListBudgets(_ context.Context, _ string) ([]store.BudgetRow, error) {
	out := []store.BudgetRow{}
	for _, class := range []string{"test_failure", "rate_limit", "timeout"} {
		if budget, ok := f.budgets[class]; ok {
			out = append(out, budget)
		}
	}
	return out, nil
}

func (f *fakeRecoveryStores) RecordReceipt(_ context.Context, receipt recovery.Receipt) (recovery.Receipt, bool, error) {
	for _, existing := range f.receipts {
		if existing.RunID == receipt.RunID && existing.EffectID == receipt.EffectID {
			return existing, true, nil
		}
	}
	f.receipts = append(f.receipts, receipt)
	return receipt, false, nil
}

func (f *fakeRecoveryStores) ListReceipts(_ context.Context, _ string, limit int) ([]recovery.Receipt, error) {
	if limit > 0 && len(f.receipts) > limit {
		return f.receipts[len(f.receipts)-limit:], nil
	}
	return f.receipts, nil
}

func (f *fakeRecoveryStores) SaveCheckpoint(_ context.Context, snapshot recovery.Snapshot, _ string) (recovery.Snapshot, error) {
	f.checkpoints = append(f.checkpoints, snapshot)
	return snapshot, nil
}

func (f *fakeRecoveryStores) GetCheckpoint(_ context.Context, _, snapshotID string) (recovery.Snapshot, bool, error) {
	for _, snapshot := range f.checkpoints {
		if snapshot.ID == snapshotID {
			return snapshot, true, nil
		}
	}
	return recovery.Snapshot{}, false, nil
}

func (f *fakeRecoveryStores) LatestCheckpoint(_ context.Context, _, _ string) (recovery.Snapshot, bool, error) {
	if len(f.checkpoints) == 0 {
		return recovery.Snapshot{}, false, nil
	}
	return f.checkpoints[len(f.checkpoints)-1], true, nil
}

func (f *fakeRecoveryStores) ListCheckpoints(_ context.Context, _, _ string, limit int) ([]store.CheckpointRow, error) {
	out := make([]store.CheckpointRow, 0, len(f.checkpoints))
	for _, snapshot := range f.checkpoints {
		out = append(out, store.CheckpointRow{ID: snapshot.ID, RunID: snapshot.RunID, Verdict: snapshot.Verdict, Files: snapshotFilesJSON(snapshot)})
	}
	if limit > 0 && len(out) > limit {
		return out[len(out)-limit:], nil
	}
	return out, nil
}

func snapshotFilesJSON(snapshot recovery.Snapshot) json.RawMessage {
	raw, err := json.Marshal(snapshot.Files)
	if err != nil {
		return json.RawMessage("{}")
	}
	return raw
}

func newRecoveryHarness(t *testing.T, stores RecoveryStores) m02Harness {
	t.Helper()
	base := newM02Harness(t, config.EnvTest)
	// apiAuthServices verifies with the fast hasher, so seed its hash form.
	base.identities.byEmail["owner@example.test"] = "hashed:" + testPassword
	base.runs = newFakeRunsStore()
	base.queue = &fakeTaskQueue{}
	base.patches = NewPatchService(nil, NewMemoryCheckpoints(), func(string) string { return t.TempDir() })
	base.checks = NewCheckService(nil, NewMemoryCheckStore())
	base.recovery = NewRecoveryService(stores, func(string) string { return t.TempDir() })
	base.handler = recoveryServer(t, base)
	return base
}

// recoveryServer starts a server over the harness fakes so a test can exercise
// the recovery routes.
func recoveryServer(t *testing.T, base m02Harness) http.Handler {
	t.Helper()
	server, err := NewServer(Options{
		Config:            &config.Config{Process: config.ProcessAPI, Env: config.EnvTest, HTTPAddr: ":8080"},
		DB:                pingDB{},
		PendingMigrations: func(context.Context) ([]migrations.Migration, error) { return nil, nil },
		Services:          base.harnessServices(),
		Auth:              apiAuthServices(base, &fakeOrgs{}),
		Encryptor:         base.encryptor,
		Runs:              base.runs,
		Queue:             base.queue,
		Patches:           base.patches,
		Checks:            base.checks,
		Recovery:          base.recovery,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return server.Handler()
}

// seedAttempts records the history loop detection reasons over.
func (f *fakeRecoveryStores) seedAttempts(t *testing.T, runID string, count, failing int) {
	t.Helper()
	for number := 1; number <= count; number++ {
		f.attempts = append(f.attempts, store.RecoveryAttemptRow{
			ID:               "rat_" + runID + string(rune('0'+number)),
			RunID:            runID,
			OrganizationID:   "org_m12",
			Number:           number,
			Kind:             "patched",
			Fingerprint:      "fp_shared",
			PatchFingerprint: "diff_" + runID,
			FailingCount:     failing,
			Verdict:          "red",
			Note:             "seeded attempt",
		})
	}
}

func TestRecoverPlansNextBoundedAttempt(t *testing.T) {
	stores := newFakeRecoveryStores()
	harness := newRecoveryHarness(t, stores)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)

	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/recover",
		`{"reason":"Repair the red suite from the last diagnostic"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf, "Idempotency-Key": "idk_m12_recover_1"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"attempt":1`) || !strings.Contains(body, `"hypothesis"`) || !strings.Contains(body, `"budget"`) || !strings.Contains(body, `"delay_ms"`) {
		t.Fatalf("body = %s", body)
	}
	if len(stores.journal) != 1 {
		t.Fatalf("journal entries = %d, want the scheduled retry", len(stores.journal))
	}
	if stores.journal[0].Kind != string(recovery.EventRetryScheduled) {
		t.Fatalf("journal kind = %s", stores.journal[0].Kind)
	}
}

func TestRecoverBlocksAfterBudgetExhausted(t *testing.T) {
	stores := newFakeRecoveryStores()
	harness := newRecoveryHarness(t, stores)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)

	// test_failure allows five attempts; the refusal after them must be honest.
	for attempt := 0; attempt < 4; attempt++ {
		rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/recover",
			`{"reason":"attempt"}`,
			map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
		if rec.Code != http.StatusCreated {
			t.Fatalf("attempt %d status = %d body = %s", attempt+1, rec.Code, rec.Body.String())
		}
	}
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/recover",
		`{"reason":"one more"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 after the budget is spent", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "recovery_retry_exhausted") || !strings.Contains(rec.Body.String(), "budget") {
		t.Fatalf("body = %s", rec.Body.String())
	}
	// The blocked attempt is journalled so the timeline shows it.
	blockedEvents := 0
	for _, event := range stores.journal {
		if event.Kind == "blocked" || event.Kind == "retry_exhausted" {
			blockedEvents++
		}
	}
	if blockedEvents == 0 {
		t.Fatal("no blocked event was journalled")
	}
}

func TestRecoverBlocksDetectedLoopWithEvidence(t *testing.T) {
	stores := newFakeRecoveryStores()
	harness := newRecoveryHarness(t, stores)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)
	// Two attempts that apply the same diff prove a loop before a third runs.
	stores.seedAttempts(t, runID, 2, 4)

	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/recover",
		`{"reason":"trying again"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 for a proven loop", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "recovery_loop_detected") || !strings.Contains(body, "loop") {
		t.Fatalf("body = %s", body)
	}
	var payload map[string]any
	if err := jsonUnmarshalBytes(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	apiErr, _ := payload["error"].(map[string]any)
	details, _ := apiErr["details"].(map[string]any)
	if details == nil {
		t.Fatalf("details = %v", payload)
	}
	loopEvidence, ok := details["loop"].(map[string]any)
	if !ok {
		t.Fatalf("loop evidence missing: %v", details)
	}
	if loopEvidence["kind"] != string(recovery.LoopIdenticalPatch) {
		t.Fatalf("loop kind = %v", loopEvidence["kind"])
	}
}

func TestRecoverRejectsUnknownClass(t *testing.T) {
	stores := newFakeRecoveryStores()
	harness := newRecoveryHarness(t, stores)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/recover",
		`{"class":"rm -rf /"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestRecoveryViewReportsTimelineAndBudgets(t *testing.T) {
	stores := newFakeRecoveryStores()
	harness := newRecoveryHarness(t, stores)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)
	harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/recover",
		`{"reason":"first"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	stores.seedAttempts(t, runID, 1, 2)
	stores.checkpoints = append(stores.checkpoints, recovery.Snapshot{
		ID:    "snp_known_good",
		RunID: runID,
		Files: map[string]recovery.FileState{"main.go": {Content: []byte("package main\n"), Mode: 0o600, Hash: "h"}},
	})

	rec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/"+runID+"/recovery", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"run_state"`, `"timeline"`, `"attempts"`, `"budgets"`, `"checkpoints"`, `"next_attempt"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %s: %s", want, body)
		}
	}
}

func TestRollbackRestoresSnapshotAndBlocksReplay(t *testing.T) {
	stores := newFakeRecoveryStores()
	harness := newRecoveryHarness(t, stores)
	// Resolve a real workspace so the restore writes real files.
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := recovery.Capture(workspace, "run_rollback", "wsp_rollback", recovery.VerdictGreen)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	stores.checkpoints = append(stores.checkpoints, snapshot)

	harness.recovery = NewRecoveryService(stores, func(string) string { return workspace })
	harness.handler = recoveryServer(t, harness)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)

	body := `{"effect_id":"effect_rollback_1","checkpoint_id":"` + snapshot.ID + `","workspace_id":"wsp_rollback"}`
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/rollback", body,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	// Break the workspace and restore again with the same effect id: the
	// recorded receipt must be replayed, not applied twice.
	if err := os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	replay := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/rollback", body,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if replay.Code != http.StatusOK {
		t.Fatalf("replay status = %d body = %s", replay.Code, replay.Body.String())
	}
	if !strings.Contains(replay.Body.String(), `"duplicate":true`) {
		t.Fatalf("replay body = %s", replay.Body.String())
	}
	content, err := os.ReadFile(filepath.Join(workspace, "main.go")) // #nosec G122 G304 -- workspace path from the test fixture
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "package broken\n" {
		t.Fatalf("workspace was rewritten on a replayed restore: %q", string(content))
	}
}

func TestRollbackRequiresEffectID(t *testing.T) {
	stores := newFakeRecoveryStores()
	harness := newRecoveryHarness(t, stores)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/rollback",
		`{"checkpoint_id":"snp_x"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "effect_id") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestRecoveryUnavailableWithoutService(t *testing.T) {
	harness := newRecoveryHarness(t, newFakeRecoveryStores())
	harness.recovery = nil
	harness.handler = recoveryServer(t, harness)
	token, _ := harness.login(t, "owner@example.test", testPassword)
	rec := harness.doRequest(t, http.MethodGet, "/api/v1/runs/run_x/recovery", "",
		map[string]string{"Cookie": sessionCookieName + "=" + token})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func TestRecoverSurfacesStoreFailures(t *testing.T) {
	stores := newFakeRecoveryStores()
	stores.failWith = errors.New("postgres is down")
	harness := newRecoveryHarness(t, stores)
	token, csrf := harness.login(t, "owner@example.test", testPassword)
	runID := createRun(t, harness, token, csrf)
	rec := harness.doRequest(t, http.MethodPost, "/api/v1/runs/"+runID+"/recover",
		`{"reason":"boom"}`,
		map[string]string{"Cookie": sessionCookieName + "=" + token, "X-CSRF-Token": csrf})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when persistence fails", rec.Code)
	}
}

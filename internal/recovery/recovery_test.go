package recovery

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/model"
)

func TestClassifierMapsKnownFailures(t *testing.T) {
	classifier := NewClassifier()
	cases := []struct {
		name string
		err  error
		want ErrorClass
	}{
		{"rate limit sentinel", model.ErrProviderRateLimit, ClassRateLimit},
		{"timeout sentinel", model.ErrProviderTimeout, ClassTimeout},
		{"provider server sentinel", model.ErrProviderServer, ClassProviderServer},
		{"transport sentinel", model.ErrProviderTransport, ClassProviderTransport},
		{"empty completion", model.ErrEmptyCompletion, ClassProviderResponse},
		{"tool args", model.ErrToolArgsInvalid, ClassToolError},
		{"provider 429 status", model.NewProviderError(http.StatusTooManyRequests, "rate_limited", "too many"), ClassRateLimit},
		{"provider 504 status", model.NewProviderError(http.StatusGatewayTimeout, "timeout", "slow"), ClassTimeout},
		{"provider 500 status", model.NewProviderError(http.StatusInternalServerError, "server", "boom"), ClassProviderServer},
		{"patch conflict", errors.New("patch hunk failed: the file changed"), ClassPatchConflict},
		{"protected path", errors.New("refusing to write a protected path"), ClassPatchRejected},
		{"expired credential", errors.New("github credential is expired"), ClassCredentialExpired},
		{"lease lost", errors.New("lease is no longer held by worker"), ClassLeaseLost},
		{"stream interrupted", errors.New("sse stream closed before completion"), ClassSSEInterrupted},
		{"container lost", errors.New("container vanished mid-flight"), ClassWorkerLost},
		{"unknown", errors.New("something odd"), ClassUnknown},
		{"nil", nil, ClassUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifier.Classify(tc.err); got != tc.want {
				t.Fatalf("Classify(%v) = %s, want %s", tc.err, got, tc.want)
			}
		})
	}
}

func TestUnknownClassIsNotRetried(t *testing.T) {
	classifier := NewClassifier()
	policy, ok := classifier.Policy(ClassUnknown)
	if !ok {
		t.Fatal("no policy for the unknown class")
	}
	if policy.MaxAttempts != 0 {
		t.Fatalf("unknown class MaxAttempts = %d, want 0", policy.MaxAttempts)
	}
	policy, _ = classifier.Policy(ClassRateLimit)
	if policy.MaxAttempts < 1 {
		t.Fatal("rate limit policy must allow at least one retry")
	}
}

func TestDelayIsBoundedAndGrows(t *testing.T) {
	policy := DefaultPolicies()[ClassRateLimit]
	first := Delay(policy, 1, 0)
	second := Delay(policy, 2, 0)
	third := Delay(policy, 3, 0)
	if first >= second || second >= third {
		t.Fatalf("delays did not grow: %v %v %v", first, second, third)
	}
	if Delay(policy, 20, 0) != policy.MaxDelay {
		t.Fatal("delay exceeded the cap")
	}
	if got := Delay(policy, 3, 10*time.Second); got != 10*time.Second {
		t.Fatalf("retry-after not honored: %v", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := ParseRetryAfter("7"); got != 7*time.Second {
		t.Fatalf("seconds form = %v", got)
	}
	if got := ParseRetryAfter(""); got != 0 {
		t.Fatalf("empty form = %v", got)
	}
	if got := ParseRetryAfter("nonsense"); got != 0 {
		t.Fatalf("invalid form = %v", got)
	}
}

func TestFingerprintIgnoresTimingNoise(t *testing.T) {
	stable := Fingerprint("--- FAIL: TestAdd (0.00s)", "2026-01-01T00:00:00Z main_test.go:15")
	noisy := Fingerprint("--- FAIL: TestAdd (1.53s)", "2026-02-03T12:13:14Z main_test.go:15")
	if stable != noisy {
		t.Fatalf("fingerprints differ on timing noise: %s vs %s", stable, noisy)
	}
	other := Fingerprint("--- FAIL: TestSub", "main_test.go:20")
	if stable == other {
		t.Fatal("distinct failures must not share a fingerprint")
	}
}

func TestBudgetConsumption(t *testing.T) {
	budget := Budget{Class: ClassTestFailure, Allowed: 2}
	budget = budget.Consume()
	if budget.Remaining != 1 || budget.Exhausted {
		t.Fatalf("after first consume: %+v", budget)
	}
	budget = budget.Consume()
	if !budget.Exhausted || budget.Remaining != 0 {
		t.Fatalf("after second consume: %+v", budget)
	}
}

func TestAssessRefusesExhaustedBudget(t *testing.T) {
	engine := NewEngine(nil, nil, nil)
	history := []Attempt{{Number: 1}, {Number: 2}, {Number: 3}, {Number: 4}, {Number: 5}}
	_, budget, finding, err := engine.Assess(ClassTestFailure, history)
	if err != ErrBudgetExhausted {
		t.Fatalf("err = %v, want budget exhausted", err)
	}
	if !budget.Exhausted {
		t.Fatal("budget must be exhausted")
	}
	if finding != nil {
		t.Fatalf("finding = %+v, want nil", finding)
	}
}

func TestAssessAllowsProgress(t *testing.T) {
	engine := NewEngine(nil, nil, nil)
	_, _, finding, err := engine.Assess(ClassTestFailure, nil)
	if err != nil || finding != nil {
		t.Fatalf("fresh history should be retryable: err=%v finding=%+v", err, finding)
	}
}

func TestDetectorFindsIdenticalPatchLoop(t *testing.T) {
	detector := NewDetector()
	history := []Attempt{
		{RunID: "run_1", Number: 1, Kind: AttemptPatched, PatchFingerprint: "abc", FailingCount: 1},
		{RunID: "run_1", Number: 2, Kind: AttemptPatched, PatchFingerprint: "abc", FailingCount: 1},
	}
	finding := detector.Find(history)
	if finding == nil {
		t.Fatal("identical patch loop not detected")
	}
	if finding.Kind != LoopIdenticalPatch {
		t.Fatalf("kind = %s, want %s", finding.Kind, LoopIdenticalPatch)
	}
	if finding.Evidence == "" || finding.Attempts != 2 {
		t.Fatalf("finding evidence = %+v", finding)
	}
}

func TestDetectorFindsNoTestDelta(t *testing.T) {
	detector := NewDetector()
	history := []Attempt{
		{Number: 1, Kind: AttemptPatched, PatchFingerprint: "a", FailingCount: 2, Verdict: VerdictRed},
		{Number: 2, Kind: AttemptPatched, PatchFingerprint: "b", FailingCount: 2, Verdict: VerdictRed},
		{Number: 3, Kind: AttemptPatched, PatchFingerprint: "c", FailingCount: 2, Verdict: VerdictRed},
		{Number: 4, Kind: AttemptPatched, PatchFingerprint: "d", FailingCount: 2, Verdict: VerdictRed},
	}
	finding := detector.Find(history)
	if finding == nil || finding.Kind != LoopNoTestDelta {
		t.Fatalf("finding = %+v, want no_test_delta", finding)
	}
}

func TestDetectorFindsUnchangedError(t *testing.T) {
	detector := NewDetector()
	history := []Attempt{
		{Number: 1, Kind: AttemptPatched, PatchFingerprint: "a", Fingerprint: "fp", FailingCount: 1, Verdict: VerdictRed},
		{Number: 2, Kind: AttemptPatched, PatchFingerprint: "b", Fingerprint: "fp", FailingCount: 1, Verdict: VerdictRed},
		{Number: 3, Kind: AttemptPatched, PatchFingerprint: "c", Fingerprint: "fp", FailingCount: 1, Verdict: VerdictRed},
	}
	finding := detector.Find(history)
	if finding == nil || finding.Kind != LoopUnchangedError {
		t.Fatalf("finding = %+v, want unchanged_error", finding)
	}
}

func TestDetectorAllowsImprovingHistory(t *testing.T) {
	detector := NewDetector()
	history := []Attempt{
		{Number: 1, Kind: AttemptPatched, PatchFingerprint: "a", Fingerprint: "fp1", FailingCount: 4, Verdict: VerdictRed},
		{Number: 2, Kind: AttemptPatched, PatchFingerprint: "b", Fingerprint: "fp2", FailingCount: 2, Verdict: VerdictRed},
		{Number: 3, Kind: AttemptPatched, PatchFingerprint: "c", Fingerprint: "fp3", FailingCount: 1, Verdict: VerdictRed},
	}
	if finding := detector.Find(history); finding != nil {
		t.Fatalf("improving history flagged as loop: %+v", finding)
	}
}

func TestJournalAppendsAndLists(t *testing.T) {
	journal := NewMemoryJournal()
	ctx := context.Background()
	for _, kind := range []EventKind{EventRetryScheduled, EventRollback, EventRecovered} {
		if err := journal.Append(ctx, Event{ID: NewEventID(), RunID: "run_1", Kind: kind, Reason: "x", CreatedAt: nowRFC3339()}); err != nil {
			t.Fatalf("append %s: %v", kind, err)
		}
	}
	if err := journal.Append(ctx, Event{RunID: "run_1"}); !errors.Is(err, ErrJournalInvalid) {
		t.Fatalf("append without id = %v, want ErrJournalInvalid", err)
	}
	events, err := journal.List(ctx, "run_1", 2)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 2 || events[0].Kind != EventRollback {
		t.Fatalf("events = %+v", events)
	}
}

func TestReceiptReplayPreventsDuplicateEffect(t *testing.T) {
	store := NewMemoryReceipts()
	ctx := context.Background()
	receipt := Receipt{ID: NewReceiptID(), RunID: "run_1", Operation: "apply_patch", EffectID: "effect_1", Status: "applied", Result: map[string]any{"files": 2}, CreatedAt: nowRFC3339()}
	first, duplicate, err := store.Record(ctx, receipt)
	if err != nil || duplicate {
		t.Fatalf("first record: dup=%v err=%v", duplicate, err)
	}
	second, duplicate, err := store.Record(ctx, Receipt{ID: NewReceiptID(), RunID: "run_1", Operation: "apply_patch", EffectID: "effect_1", Status: "applied", CreatedAt: nowRFC3339()})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !duplicate {
		t.Fatal("replayed effect must be reported as duplicate")
	}
	if second.ID != first.ID {
		t.Fatalf("replayed receipt id = %s, want %s", second.ID, first.ID)
	}
	if stored, ok, _ := store.Get(ctx, "run_1", "effect_1"); !ok || stored.Result == nil {
		t.Fatalf("stored receipt = %+v ok=%v", stored, ok)
	}
	if _, _, err := store.Record(ctx, Receipt{RunID: "run_1"}); !errors.Is(err, ErrReceiptInvalid) {
		t.Fatalf("record with empty id = %v, want ErrReceiptInvalid", err)
	}
}

func TestJournalAndReceiptsAreConcurrencySafe(t *testing.T) {
	journal := NewMemoryJournal()
	store := NewMemoryReceipts()
	ctx := context.Background()
	done := make(chan error, 16)
	for i := 0; i < 8; i++ {
		go func(i int) {
			done <- journal.Append(ctx, Event{ID: NewEventID(), RunID: "run_c", Kind: EventRetryScheduled, CreatedAt: nowRFC3339()})
		}(i)
		go func(i int) {
			_, _, err := store.Record(ctx, Receipt{ID: NewReceiptID(), RunID: "run_c", Operation: "apply_patch", EffectID: "effect_shared", CreatedAt: nowRFC3339()})
			done <- err
		}(i)
	}
	for i := 0; i < 16; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent write: %v", err)
		}
	}
	events, err := journal.List(ctx, "run_c", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 8 {
		t.Fatalf("events = %d, want 8", len(events))
	}
	receipts, err := store.List(ctx, "run_c", 0)
	if err != nil {
		t.Fatalf("receipts: %v", err)
	}
	if len(receipts) != 1 {
		t.Fatalf("receipts = %d, want exactly one regardless of concurrent writes", len(receipts))
	}
}

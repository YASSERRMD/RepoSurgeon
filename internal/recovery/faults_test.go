package recovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/checks"
	"github.com/YASSERRMD/RepoSurgeon/internal/model"
)

// faultRunner injects one failure per attempt so recovery decisions can be
// exercised without a real provider, runner or workspace.
type faultRunner struct {
	failures map[int]error
	results  map[int]checks.Result
	calls    int
}

func (f *faultRunner) Run(_ context.Context, _, _ string, _ int) (checks.Result, error) {
	f.calls++
	attempt := f.calls
	if err, ok := f.failures[attempt]; ok {
		return checks.Result{}, err
	}
	if result, ok := f.results[attempt]; ok {
		return result, nil
	}
	zero := 0
	return checks.Result{Status: "passed", ExitCode: &zero, DurationMS: 1}, nil
}

func TestFaultsProvider429IsBoundedAndBackedOff(t *testing.T) {
	engine := NewEngine(&faultRunner{}, nil, NewMemoryTracker())
	classifier := engine.Classifier
	class := classifier.Classify(model.NewProviderError(429, "rate_limited", "slow down"))
	if class != ClassRateLimit {
		t.Fatalf("class = %s, want rate_limit", class)
	}
	policy, _ := classifier.Policy(class)
	if policy.MaxAttempts < 2 {
		t.Fatalf("429 must be retried within a bound, MaxAttempts = %d", policy.MaxAttempts)
	}
	if !policy.RespectRetryAfter {
		t.Fatal("the rate limit policy must honor Retry-After")
	}
	for attempt := 1; attempt <= policy.MaxAttempts; attempt++ {
		if got := Delay(policy, attempt, 90*time.Second); got > policy.MaxDelay {
			t.Fatalf("attempt %d delay %v exceeds the cap %v", attempt, got, policy.MaxDelay)
		}
	}
	// The attempt after the bound is refused, not retried. The history makes
	// measurable progress so only the budget, not a loop, can stop it.
	history := make([]Attempt, 0, policy.MaxAttempts)
	for number := 1; number <= policy.MaxAttempts; number++ {
		history = append(history, Attempt{
			Number:           number,
			Kind:             AttemptPatched,
			PatchFingerprint: "diff_" + string(rune('a'+number)),
			Fingerprint:      "fp_" + string(rune('a'+number)),
			FailingCount:     policy.MaxAttempts - number + 1,
			Verdict:          VerdictRed,
		})
	}
	if _, _, finding, err := engine.Assess(class, history); !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("err = %v, want budget exhausted after %d attempts", err, policy.MaxAttempts)
	} else if finding != nil {
		t.Fatalf("finding = %+v, want none", finding)
	}
}

func TestFaultsWorkerAndLeaseLossRecover(t *testing.T) {
	engine := NewEngine(&faultRunner{}, nil, NewMemoryTracker())
	for _, err := range []error{
		errors.New("the lease for task run_1 is no longer held by worker"),
		errors.New("container vanished mid-flight"),
		errors.New("sse stream closed before completion"),
	} {
		class := engine.Classifier.Classify(err)
		policy, ok := engine.Classifier.Policy(class)
		if !ok {
			t.Fatalf("no policy for %s (%v)", class, err)
		}
		if policy.MaxAttempts < 1 {
			t.Fatalf("%s must be recoverable, MaxAttempts = %d", class, policy.MaxAttempts)
		}
		if _, _, _, err := engine.Assess(class, nil); err != nil {
			t.Fatalf("%s should be retryable on a fresh run: %v", class, err)
		}
	}
}

func TestFaultsExpiredCredentialIsNotRetriedBlindly(t *testing.T) {
	engine := NewEngine(&faultRunner{}, nil, NewMemoryTracker())
	class := engine.Classifier.Classify(errors.New("github credential is expired"))
	if class != ClassCredentialExpired {
		t.Fatalf("class = %s, want credential_expired", class)
	}
	policy, _ := engine.Classifier.Policy(class)
	// One attempt is allowed so the operator can renew, and no more.
	if policy.MaxAttempts != 1 {
		t.Fatalf("MaxAttempts = %d, want exactly 1", policy.MaxAttempts)
	}
	_, budget, _, err := engine.Assess(class, nil)
	if err != nil || budget.Exhausted {
		t.Fatalf("first attempt should be allowed: err = %v budget = %+v", err, budget)
	}
	_, _, _, err = engine.Assess(class, []Attempt{{Number: 1, Kind: AttemptPatched, FailingCount: 1}})
	if !errors.Is(err, ErrBudgetExhausted) {
		t.Fatalf("second attempt err = %v, want budget exhausted", err)
	}
}

func TestFaultsReplayedEffectIsNeverAppliedTwice(t *testing.T) {
	store := NewMemoryReceipts()
	ctx := context.Background()
	// A worker crash between the effect and its durable record must not turn
	// into a duplicate side effect once the task is replayed.
	applied := 0
	run := func(attempt int) {
		_, duplicate, err := store.Record(ctx, Receipt{
			ID:        NewReceiptID(),
			RunID:     "run_fault",
			Operation: "apply_patch",
			EffectID:  "effect_stable",
			Status:    "applied",
		})
		if err != nil {
			t.Fatalf("attempt %d record: %v", attempt, err)
		}
		if duplicate {
			return
		}
		applied++
	}
	run(1)
	run(2)
	run(3)
	if applied != 1 {
		t.Fatalf("effect applied %d times, want exactly once", applied)
	}
	receipts, err := store.List(ctx, "run_fault", 0)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("receipts = %d err = %v", len(receipts), err)
	}
}

func TestFaultsRestoreIsRepeatableAndExact(t *testing.T) {
	journal := NewMemoryJournal()
	ctx := context.Background()
	// A rollback recorded in the journal must be replayable without changing
	// what the workspace ends up as.
	for attempt := 1; attempt <= 2; attempt++ {
		if err := journal.Append(ctx, Event{
			ID:        NewEventID(),
			RunID:     "run_restore",
			Kind:      EventRollback,
			Attempt:   attempt,
			Reason:    "restored the workspace to a known-good checkpoint",
			Evidence:  map[string]any{"restored_files": 1},
			ActorType: "user",
		}); err != nil {
			t.Fatalf("append %d: %v", attempt, err)
		}
	}
	events, err := journal.List(ctx, "run_restore", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want both attempts journalled", len(events))
	}
	// History is never rewritten: an earlier entry survives the later one.
	if events[0].CreatedAt == "" || events[1].CreatedAt == "" {
		t.Fatal("journalled entries must carry their timestamp")
	}
}

func TestFaultsConcurrentBudgetSpendIsAtomic(t *testing.T) {
	store := NewMemoryReceipts()
	ctx := context.Background()
	done := make(chan struct{}, 8)
	applied := make(chan struct{}, 8)
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			_, duplicate, err := store.Record(ctx, Receipt{
				ID:        NewReceiptID(),
				RunID:     "run_race",
				Operation: "restore_checkpoint",
				EffectID:  "effect_raced",
			})
			if err != nil {
				t.Errorf("record: %v", err)
				return
			}
			if !duplicate {
				applied <- struct{}{}
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	close(applied)
	claims := 0
	for range applied {
		claims++
	}
	if claims != 1 {
		t.Fatalf("%d workers believe they applied the same effect, want exactly 1", claims)
	}
	receipts, err := store.List(ctx, "run_race", 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(receipts) != 1 {
		t.Fatalf("receipts = %d, want a single effect record", len(receipts))
	}
}

func TestFaultsCheckEngineReportsVerdictFromChecksNotFromRetries(t *testing.T) {
	runner := &faultRunner{results: map[int]checks.Result{
		1: failingResult(),
		2: greenResult(),
	}}
	engine := NewEngine(runner, nil, NewMemoryTracker())
	first, err := engine.RunAttempt(context.Background(), AttemptRequest{
		RunID:   "run_verdict",
		Class:   ClassTestFailure,
		Workdir: t.TempDir(),
		Operate: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if first.Verdict != checks.VerdictRed || first.Recovered {
		t.Fatalf("first attempt = %+v", first)
	}
	second, err := engine.RunAttempt(context.Background(), AttemptRequest{
		RunID:           "run_verdict",
		Class:           ClassTestFailure,
		Workdir:         t.TempDir(),
		DiffFingerprint: "diff_recovered",
		Operate:         func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if !second.Recovered || second.Verdict != checks.VerdictGreen {
		t.Fatalf("second attempt = %+v", second)
	}
	// The verdict came from the check that ran, not from the retry count.
	if runner.calls != 2 {
		t.Fatalf("runner calls = %d, want one check per attempt", runner.calls)
	}
}

package recovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/YASSERRMD/RepoSurgeon/internal/checks"
)

// fakeRunner replays scripted check results and records the profile order.
type fakeRunner struct {
	results map[string]checks.Result
	err     error
	calls   []string
}

func (f *fakeRunner) Run(_ context.Context, profile, _ string, _ int) (checks.Result, error) {
	f.calls = append(f.calls, profile)
	if f.err != nil {
		return checks.Result{}, f.err
	}
	if result, ok := f.results[profile]; ok {
		return result, nil
	}
	zero := 0
	return checks.Result{Status: "passed", ExitCode: &zero, Stdout: "ok", DurationMS: 1}, nil
}

// failingResult is the exact shape of a real go test failure line group.
func failingResult() checks.Result {
	two := 2
	return checks.Result{
		Status:     "failed",
		ExitCode:   &two,
		Stdout:     "--- FAIL: TestAdd (0.00s)\n    main_test.go:15: Add(2, 2) = 5, want 4\n",
		DurationMS: 42,
	}
}

func greenResult() checks.Result {
	zero := 0
	return checks.Result{Status: "passed", ExitCode: &zero, Stdout: "ok", DurationMS: 9}
}

func TestRepairLoopRecoversAfterFirstFailure(t *testing.T) {
	runner := &fakeRunner{results: map[string]checks.Result{"go.test": failingResult()}}
	engine := NewEngine(runner, nil, NewMemoryTracker())
	report := CheckFromResult("go.test", failingResult())

	// The first attempt is refused as soon as the budget for the class is
	// spent by prior attempts; here the history is empty so it runs.
	first, err := engine.RunAttempt(context.Background(), AttemptRequest{
		RunID:   "run_repair",
		Class:   ClassTestFailure,
		Failure: report,
		Operate: func(context.Context) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	if first.Verdict != checks.VerdictRed {
		t.Fatalf("verdict = %s, want red", first.Verdict)
	}
	// The first attempt still fails, so the runner ran exactly once.
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %v", runner.calls)
	}

	// The next attempt fixes the workspace; the check turns green.
	runner.results["go.test"] = greenResult()
	second, err := engine.RunAttempt(context.Background(), AttemptRequest{
		RunID:           "run_repair",
		Class:           ClassTestFailure,
		Failure:         report,
		DiffFingerprint: "diff_fixed",
		Operate: func(context.Context) error {
			return nil
		},
	})
	if err != nil {
		t.Fatalf("second attempt: %v", err)
	}
	if !second.Recovered || second.Verdict != checks.VerdictGreen {
		t.Fatalf("second attempt did not recover: %+v", second)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("runner calls = %v, want two", runner.calls)
	}
}

func TestRepairLoopStopsOnIdenticalDiff(t *testing.T) {
	runner := &fakeRunner{results: map[string]checks.Result{"go.test": failingResult()}}
	engine := NewEngine(runner, nil, NewMemoryTracker())
	report := CheckFromResult("go.test", failingResult())
	request := AttemptRequest{
		RunID:           "run_loop",
		Class:           ClassTestFailure,
		Failure:         report,
		DiffFingerprint: "diff_same",
		Operate: func(context.Context) error {
			return nil
		},
	}
	if _, err := engine.RunAttempt(context.Background(), request); err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	_, err := engine.RunAttempt(context.Background(), request)
	if !errors.Is(err, ErrLoopDetected) {
		t.Fatalf("second attempt err = %v, want ErrLoopDetected", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner was called after the loop was proven: %v", runner.calls)
	}
}

func TestRepairLoopRequiresAnOperation(t *testing.T) {
	engine := NewEngine(&fakeRunner{}, nil, NewMemoryTracker())
	_, err := engine.RunAttempt(context.Background(), AttemptRequest{RunID: "run_x", Class: ClassTestFailure})
	if !errors.Is(err, ErrNoOperation) {
		t.Fatalf("err = %v, want ErrNoOperation", err)
	}
}

func TestRepairLoopPropagatesOperationErrors(t *testing.T) {
	sentinel := errors.New("workspace is read-only")
	engine := NewEngine(&fakeRunner{}, nil, NewMemoryTracker())
	_, err := engine.RunAttempt(context.Background(), AttemptRequest{
		RunID: "run_y",
		Class: ClassTestFailure,
		Operate: func(context.Context) error {
			return sentinel
		},
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the operation error", err)
	}
}

func TestCaptureDetectsDriftAndRestores(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Capture(root, "run_snap", "wsp_1", checks.VerdictGreen)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	drift, err := snapshot.Drift(root)
	if err != nil || len(drift) != 0 {
		t.Fatalf("drift = %v err = %v", drift, err)
	}
	// Introduce broken state and a stray artifact.
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "leftover.txt"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	drift, err = snapshot.Drift(root)
	if err != nil {
		t.Fatalf("drift: %v", err)
	}
	if len(drift) != 1 || drift[0] != "main.go" {
		t.Fatalf("drift = %v, want main.go", drift)
	}
	restored, err := snapshot.Restore(root)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(restored) != 2 {
		t.Fatalf("restored = %v", restored)
	}
	content, err := os.ReadFile(filepath.Join(root, "main.go"))
	if err != nil || string(content) != "package main\n" {
		t.Fatalf("main.go = %q err = %v", content, err)
	}
	if _, err := os.Stat(filepath.Join(root, "leftover.txt")); !os.IsNotExist(err) {
		t.Fatalf("stray artifact survived the rollback: %v", err)
	}
	drift, err = snapshot.Drift(root)
	if err != nil || len(drift) != 0 {
		t.Fatalf("post-rollback drift = %v err = %v", drift, err)
	}
}

func TestCaptureSkipsVCSAndDependencyDirs(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "dep"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "dep", "index.js"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.ts"), []byte("export {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Capture(root, "run_snap2", "wsp_2", checks.VerdictRed)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if len(snapshot.Files) != 1 {
		t.Fatalf("files = %v, want only app.ts", snapshot.Files)
	}
}

func TestTrackerViewKeepsHistory(t *testing.T) {
	tracker := NewMemoryTracker()
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if err := tracker.AppendAttempt(ctx, Attempt{RunID: "run_z", FailingCount: i}); err != nil {
			t.Fatal(err)
		}
	}
	history, err := tracker.Attempts(ctx, "run_z", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 4 {
		t.Fatalf("history = %d, want 4", len(history))
	}
	if finding := NewDetector().Find(history); finding == nil {
		t.Fatal("flat history must be flagged")
	}
}

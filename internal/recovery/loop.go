package recovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/checks"
)

// Errors returned by the repair loop. Callers translate these into HTTP
// statuses or queue decisions.
var (
	// ErrLoopDetected means the run stopped because progress was impossible.
	ErrLoopDetected = errors.New("recovery: repair loop detected")
	// ErrBudgetExhausted means the bounded retry budget for the class is spent.
	ErrBudgetExhausted = errors.New("recovery: attempt budget exhausted")
	// ErrNoOperation marks a repair request with no operation to run.
	ErrNoOperation = errors.New("recovery: repair attempt has no operation")
)

// ToolRunner runs command profiles. It mirrors checks.Runner.
type ToolRunner interface {
	Run(ctx context.Context, profile, workdir string, timeoutMS int) (checks.Result, error)
}

// PatchEngine applies a diff to a workspace root.
type PatchEngine interface {
	Apply(root, diff string) error
}

// ProgressSink records repair attempts so loop detection and the UI can see
// the full history.
type ProgressSink interface {
	AppendAttempt(ctx context.Context, attempt Attempt) error
	Attempts(ctx context.Context, runID string, limit int) ([]Attempt, error)
}

// Hypothesis is the focused, bounded plan for one repair attempt. It is
// explicit so an operator sees what will change before a patch is applied.
type Hypothesis struct {
	// Class is the failure class being repaired.
	Class ErrorClass `json:"class"`
	// Rationale explains the evidence the hypothesis rests on.
	Rationale string `json:"rationale"`
	// Profiles are the checks to rerun after the patch.
	Profiles []string `json:"profiles"`
	// Fingerprint is the failure signature being targeted.
	Fingerprint string `json:"fingerprint"`
}

// Diagnose turns a red check report into one focused hypothesis. No model is
// consulted: the plan is derived from the parsed diagnostics, so it is stable
// and explainable.
func Diagnose(class ErrorClass, report checks.Check) Hypothesis {
	profile := report.Profile
	if profile == "" {
		profile = "go.test"
	}
	fingerprint := Fingerprint(profile, report.RawLogSHA256)
	if len(report.FailureGroups) > 0 {
		first := report.FailureGroups[0]
		fingerprint = Fingerprint(profile, first.Path, first.Message)
	}
	return Hypothesis{
		Class:       class,
		Rationale:   fmt.Sprintf("repair attempt targets %d failing diagnostic group(s) then reruns %s", len(report.FailureGroups), profile),
		Profiles:    []string{profile},
		Fingerprint: fingerprint,
	}
}

// FailureKeys returns the sorted failing-test signatures of a check report so
// before/after sets can be compared exactly.
func FailureKeys(report checks.Check) []string {
	keys := make([]string, 0, len(report.FailureGroups))
	for _, group := range report.FailureGroups {
		keys = append(keys, fmt.Sprintf("%s:%d:%s", group.Path, group.Line, group.Message))
	}
	sort.Strings(keys)
	return keys
}

// AttemptRequest carries everything one repair iteration needs.
type AttemptRequest struct {
	// RunID is the run being repaired.
	RunID string
	// Class is the failure class this attempt addresses.
	Class ErrorClass
	// Workdir is the workspace root to run checks against.
	Workdir string
	// Failure is the red check the attempt targets.
	Failure checks.Check
	// DiffFingerprint identifies the diff, empty for a no-op attempt.
	DiffFingerprint string
	// RetryAfter is a provider hint, honored only where policy allows.
	RetryAfter time.Duration
	// Operate applies the diff to the workspace before checks run.
	Operate func(ctx context.Context) error
}

// AttemptOutcome is the recorded result of one attempt.
type AttemptOutcome struct {
	// Attempt is the journaled attempt.
	Attempt Attempt
	// Report is the check executed after the patch, when one ran.
	Report checks.Check
	// Budget is the class budget as of this attempt.
	Budget Budget
	// Verdict is the resulting verdict.
	Verdict string
	// Recovered reports the suite returning to green.
	Recovered bool
	// Delay is the backoff before the next permitted attempt.
	Delay time.Duration
	// Hypothesis is the focused plan this attempt executed.
	Hypothesis Hypothesis
}

// Engine drives the bounded repair loop. Every decision it makes is either a
// policy lookup or a comparison of recorded evidence.
type Engine struct {
	Classifier *Classifier
	Detector   *Detector
	Runner     ToolRunner
	Patches    PatchEngine
	Tracker    ProgressSink
}

// NewEngine builds an engine with the default policy and loop thresholds.
func NewEngine(runner ToolRunner, patches PatchEngine, tracker ProgressSink) *Engine {
	return &Engine{
		Classifier: NewClassifier(),
		Detector:   NewDetector(),
		Runner:     runner,
		Patches:    patches,
		Tracker:    tracker,
	}
}

// Assess decides what the next step for a failure is without mutating state,
// so a caller can render the decision or answer a dry-run request.
func (e *Engine) Assess(class ErrorClass, history []Attempt) (Policy, Budget, *LoopFinding, error) {
	return e.assessWithProposed(class, history, Attempt{})
}

// assessWithProposed is Assess with the not-yet-executed attempt included, so
// a duplicate action is caught before it runs.
func (e *Engine) assessWithProposed(class ErrorClass, history []Attempt, proposed Attempt) (Policy, Budget, *LoopFinding, error) {
	policy, ok := e.Classifier.Policy(class)
	if !ok {
		policy = DefaultPolicies()[ClassUnknown]
	}
	used := len(history)
	budget := Budget{Class: class, Allowed: policy.MaxAttempts, Used: used}
	if remaining := policy.MaxAttempts - used; remaining > 0 {
		budget.Remaining = remaining
	} else {
		budget.Exhausted = true
	}
	finding := e.Detector.Find(history)
	if proposed.PatchFingerprint != "" || proposed.Fingerprint != "" {
		proposed.Number = len(history) + 1
		finding = e.Detector.FindWithProposed(history, proposed)
	}
	if finding != nil {
		return policy, budget, finding, ErrLoopDetected
	}
	if budget.Exhausted {
		return policy, budget, nil, ErrBudgetExhausted
	}
	return policy, budget, nil, nil
}

// NextDelay returns the backoff before the next attempt for a class.
func (e *Engine) NextDelay(class ErrorClass, next int, retryAfter time.Duration) time.Duration {
	policy, ok := e.Classifier.Policy(class)
	if !ok {
		policy = DefaultPolicies()[ClassUnknown]
	}
	return Delay(policy, next, retryAfter)
}

// RunAttempt applies one bounded repair attempt and records it. The attempt is
// refused before any side effect when the budget is spent or a loop is proven.
func (e *Engine) RunAttempt(ctx context.Context, req AttemptRequest) (AttemptOutcome, error) {
	history := []Attempt{}
	if e.Tracker != nil {
		recorded, err := e.Tracker.Attempts(ctx, req.RunID, 0)
		if err != nil {
			return AttemptOutcome{}, err
		}
		history = recorded
	}
	proposed := Attempt{
		RunID:            req.RunID,
		Number:           len(history) + 1,
		Kind:             AttemptPatched,
		Fingerprint:      Fingerprint(req.Failure.Profile, req.Failure.RawLogSHA256),
		PatchFingerprint: req.DiffFingerprint,
		FailingCount:     len(req.Failure.FailureGroups),
		Verdict:          checks.VerdictFor(req.Failure),
	}
	policy, budget, finding, err := e.assessWithProposed(req.Class, history, proposed)
	if err != nil {
		note := err.Error()
		if finding != nil {
			note = string(finding.Kind) + ": " + finding.Evidence
		}
		attempt := Attempt{
			RunID:       req.RunID,
			Number:      len(history) + 1,
			Kind:        AttemptBlocked,
			Fingerprint: req.Failure.RawLogSHA256,
			Verdict:     checks.VerdictFor(req.Failure),
			Note:        note,
		}
		if e.Tracker != nil {
			if appendErr := e.Tracker.AppendAttempt(ctx, attempt); appendErr != nil {
				return AttemptOutcome{}, appendErr
			}
		}
		return AttemptOutcome{Attempt: attempt, Budget: budget, Verdict: attempt.Verdict}, err
	}
	if req.Operate == nil {
		return AttemptOutcome{}, ErrNoOperation
	}
	if err := req.Operate(ctx); err != nil {
		return AttemptOutcome{}, err
	}
	result, err := e.Runner.Run(ctx, req.Failure.Profile, req.Workdir, 0)
	if err != nil {
		return AttemptOutcome{}, err
	}
	report := CheckFromResult(req.Failure.Profile, result)
	verdict := checks.VerdictFor(report)
	hypothesis := Diagnose(req.Class, report)
	attempt := Attempt{
		RunID:            req.RunID,
		Number:           len(history) + 1,
		Kind:             AttemptPatched,
		Fingerprint:      hypothesis.Fingerprint,
		PatchFingerprint: req.DiffFingerprint,
		FailingCount:     len(report.FailureGroups),
		Verdict:          verdict,
		Note:             hypothesis.Rationale,
	}
	if e.Tracker != nil {
		if err := e.Tracker.AppendAttempt(ctx, attempt); err != nil {
			return AttemptOutcome{}, err
		}
	}
	return AttemptOutcome{
		Attempt:    attempt,
		Report:     report,
		Budget:     budget,
		Verdict:    verdict,
		Recovered:  verdict == checks.VerdictGreen,
		Delay:      Delay(policy, len(history)+1, req.RetryAfter),
		Hypothesis: hypothesis,
	}, nil
}

// memoryTracker is the in-process attempt history.
type memoryTracker struct {
	items map[string][]Attempt
}

// NewMemoryTracker builds an in-memory attempt tracker.
func NewMemoryTracker() ProgressSink {
	return &memoryTracker{items: map[string][]Attempt{}}
}

func (m *memoryTracker) AppendAttempt(_ context.Context, attempt Attempt) error {
	m.items[attempt.RunID] = append(m.items[attempt.RunID], attempt)
	return nil
}

func (m *memoryTracker) Attempts(_ context.Context, runID string, limit int) ([]Attempt, error) {
	history := append([]Attempt(nil), m.items[runID]...)
	if limit > 0 && len(history) > limit {
		history = history[len(history)-limit:]
	}
	return history, nil
}

// CheckFromResult turns a raw runner result into the same check shape the API
// returns, so recovery and the end-user API classify evidence identically.
func CheckFromResult(profile string, result checks.Result) checks.Check {
	groups := checks.ParseFailures(profile, result.Stdout, result.Stderr)
	raw := result.Stdout
	if result.Stderr != "" {
		raw += "\n" + result.Stderr
	}
	exitCode := 1
	if result.ExitCode != nil {
		exitCode = *result.ExitCode
	}
	check := checks.Check{
		ID:            checks.NewCheckID(),
		Profile:       profile,
		Status:        result.Status,
		ExitCode:      &exitCode,
		DurationMS:    result.DurationMS,
		FailureGroups: groups,
		RawLogSHA256:  checks.HashText(raw),
	}
	check.Verdict = checks.VerdictFor(check)
	return check
}

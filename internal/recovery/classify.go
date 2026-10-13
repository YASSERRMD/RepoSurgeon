// Package recovery classifies failures, bounds retries, detects loops, and
// drives the repair loop that turns a red check suite back into a green one.
//
// The package is deliberately dependency-light: it defines the seams the
// worker and the API satisfy (runner, patch applier, snapshotter, journal,
// receipts) so that recovery behavior can be tested without Docker, network
// access or a live model provider.
package recovery

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/model"
)

// ErrorClass groups failures that share a recovery strategy.
type ErrorClass string

// Error classes. The zero value is never a valid class.
const (
	// ClassRateLimit is HTTP 429 from the provider or GitHub.
	ClassRateLimit ErrorClass = "rate_limit"
	// ClassTimeout is a provider, runner or lease timeout.
	ClassTimeout ErrorClass = "timeout"
	// ClassProviderServer is a 5xx from the model provider.
	ClassProviderServer ErrorClass = "provider_server"
	// ClassProviderTransport is a connection-level failure of the provider.
	ClassProviderTransport ErrorClass = "provider_transport"
	// ClassProviderResponse is a malformed or empty provider answer.
	ClassProviderResponse ErrorClass = "provider_response"
	// ClassToolError is a tool broker rejection or tool-side failure.
	ClassToolError ErrorClass = "tool_error"
	// ClassPatchConflict is a patch that no longer applies cleanly.
	ClassPatchConflict ErrorClass = "patch_conflict"
	// ClassPatchRejected is a patch refused by the protected-path policy.
	ClassPatchRejected ErrorClass = "patch_rejected"
	// ClassTestFailure is a deterministic red suite with parsed diagnostics.
	ClassTestFailure ErrorClass = "test_failure"
	// ClassCredentialExpired is an expired or revoked GitHub credential.
	ClassCredentialExpired ErrorClass = "credential_expired" // #nosec G101 -- names a failure class, not a secret
	// ClassLeaseLost is a task lease stolen by another worker.
	ClassLeaseLost ErrorClass = "lease_lost"
	// ClassWorkerLost is a worker or container that disappeared mid-flight.
	ClassWorkerLost ErrorClass = "worker_lost"
	// ClassSSEInterrupted is a model stream that ended before completion.
	ClassSSEInterrupted ErrorClass = "sse_interrupted"
	// ClassUnknown is anything not yet classified; it is not retried.
	ClassUnknown ErrorClass = "unknown"
)

// Classes returns every known class in a stable order.
func Classes() []ErrorClass {
	return []ErrorClass{
		ClassRateLimit, ClassTimeout, ClassProviderServer, ClassProviderTransport,
		ClassProviderResponse, ClassToolError, ClassPatchConflict, ClassPatchRejected,
		ClassTestFailure, ClassCredentialExpired, ClassLeaseLost, ClassWorkerLost,
		ClassSSEInterrupted, ClassUnknown,
	}
}

// Known reports whether the class is one of the defined classes.
func (c ErrorClass) Known() bool {
	for _, candidate := range Classes() {
		if c == candidate {
			return true
		}
	}
	return false
}

// String renders the class for JSON payloads and audit records.
func (c ErrorClass) String() string { return string(c) }

// Policy is the bounded retry policy for one error class.
type Policy struct {
	// Class is the class the policy applies to.
	Class ErrorClass
	// MaxAttempts bounds the retries for this class per run.
	MaxAttempts int
	// BaseDelay is the delay before the first retry.
	BaseDelay time.Duration
	// MaxDelay caps the exponential backoff.
	MaxDelay time.Duration
	// Multiplier grows the delay per attempt.
	Multiplier float64
	// RespectRetryAfter honors a provider Retry-After when present.
	RespectRetryAfter bool
}

// DefaultPolicies returns the policy table of record. Every retry is bounded
// by MaxAttempts and every delay is capped, so a failing run can never spend
// unbounded time or tokens retrying.
func DefaultPolicies() map[ErrorClass]Policy {
	return map[ErrorClass]Policy{
		ClassRateLimit:         {Class: ClassRateLimit, MaxAttempts: 5, BaseDelay: 2 * time.Second, MaxDelay: 2 * time.Minute, Multiplier: 2, RespectRetryAfter: true},
		ClassTimeout:           {Class: ClassTimeout, MaxAttempts: 3, BaseDelay: 1 * time.Second, MaxDelay: 30 * time.Second, Multiplier: 2},
		ClassProviderServer:    {Class: ClassProviderServer, MaxAttempts: 4, BaseDelay: 500 * time.Millisecond, MaxDelay: 20 * time.Second, Multiplier: 2},
		ClassProviderTransport: {Class: ClassProviderTransport, MaxAttempts: 3, BaseDelay: 250 * time.Millisecond, MaxDelay: 10 * time.Second, Multiplier: 2},
		ClassProviderResponse:  {Class: ClassProviderResponse, MaxAttempts: 2, BaseDelay: 250 * time.Millisecond, MaxDelay: 2 * time.Second, Multiplier: 1},
		ClassToolError:         {Class: ClassToolError, MaxAttempts: 3, BaseDelay: 0, MaxDelay: 5 * time.Second, Multiplier: 1},
		ClassPatchConflict:     {Class: ClassPatchConflict, MaxAttempts: 4, BaseDelay: 0, MaxDelay: 2 * time.Second, Multiplier: 1},
		ClassPatchRejected:     {Class: ClassPatchRejected, MaxAttempts: 1, BaseDelay: 0, MaxDelay: 0, Multiplier: 1},
		ClassTestFailure:       {Class: ClassTestFailure, MaxAttempts: 5, BaseDelay: 0, MaxDelay: 2 * time.Second, Multiplier: 1},
		ClassCredentialExpired: {Class: ClassCredentialExpired, MaxAttempts: 1, BaseDelay: 0, MaxDelay: 0, Multiplier: 1},
		ClassLeaseLost:         {Class: ClassLeaseLost, MaxAttempts: 2, BaseDelay: 1 * time.Second, MaxDelay: 10 * time.Second, Multiplier: 2},
		ClassWorkerLost:        {Class: ClassWorkerLost, MaxAttempts: 2, BaseDelay: 2 * time.Second, MaxDelay: 30 * time.Second, Multiplier: 2},
		ClassSSEInterrupted:    {Class: ClassSSEInterrupted, MaxAttempts: 3, BaseDelay: 250 * time.Millisecond, MaxDelay: 5 * time.Second, Multiplier: 2},
		ClassUnknown:           {Class: ClassUnknown, MaxAttempts: 0, BaseDelay: 0, MaxDelay: 0, Multiplier: 1},
	}
}

// Classifier maps an arbitrary error onto an ErrorClass.
type Classifier struct {
	policies map[ErrorClass]Policy
}

// NewClassifier builds a classifier from the default policy table.
func NewClassifier() *Classifier {
	return &Classifier{policies: DefaultPolicies()}
}

// Policy returns the retry policy for a class.
func (c *Classifier) Policy(class ErrorClass) (Policy, bool) {
	p, ok := c.policies[class]
	return p, ok
}

// Classify resolves the class of an error. HTTP status codes, model sentinels
// and plain messages all participate, so callers never parse strings
// themselves.
func (c *Classifier) Classify(err error) ErrorClass {
	if err == nil {
		return ClassUnknown
	}
	var providerErr *model.ProviderError
	if errors.As(err, &providerErr) {
		switch providerErr.Status {
		case http.StatusTooManyRequests:
			return ClassRateLimit
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			return ClassTimeout
		default:
			if providerErr.Status >= 500 {
				return ClassProviderServer
			}
		}
		message := strings.ToLower(providerErr.Message)
		switch {
		case strings.Contains(message, "rate limit"), strings.Contains(message, "retry-after"):
			return ClassRateLimit
		case strings.Contains(message, "stream"), strings.Contains(message, "sse"):
			return ClassSSEInterrupted
		}
	}
	switch {
	case errors.Is(err, model.ErrProviderRateLimit):
		return ClassRateLimit
	case errors.Is(err, model.ErrProviderTimeout):
		return ClassTimeout
	case errors.Is(err, model.ErrProviderServer):
		return ClassProviderServer
	case errors.Is(err, model.ErrProviderTransport):
		return ClassProviderTransport
	case errors.Is(err, model.ErrEmptyCompletion), errors.Is(err, model.ErrProviderResponse):
		return ClassProviderResponse
	case errors.Is(err, model.ErrToolArgsInvalid), errors.Is(err, model.ErrUnsupportedTool):
		return ClassToolError
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "429"), strings.Contains(message, "rate limit"):
		return ClassRateLimit
	case strings.Contains(message, "lease"):
		return ClassLeaseLost
	case strings.Contains(message, "context deadline exceeded"), strings.Contains(message, "timed out"), strings.Contains(message, "timeout"):
		return ClassTimeout
	case strings.Contains(message, "credential"), strings.Contains(message, "401"), strings.Contains(message, "unauthorized"), strings.Contains(message, "expired"):
		return ClassCredentialExpired
	case strings.Contains(message, "sse"), strings.Contains(message, "interrupted stream"), strings.Contains(message, "stream closed"):
		return ClassSSEInterrupted
	case strings.Contains(message, "hunk"), strings.Contains(message, "does not apply"), strings.Contains(message, "patch can no longer"), strings.Contains(message, "conflict"):
		return ClassPatchConflict
	case strings.Contains(message, "protected"), strings.Contains(message, "path escapes"):
		return ClassPatchRejected
	case strings.Contains(message, "container"), strings.Contains(message, "worker"):
		return ClassWorkerLost
	case strings.Contains(message, "tool"):
		return ClassToolError
	}
	return ClassUnknown
}

// Fingerprint normalises free-form diagnostics into a stable hash so that the
// same failure is recognized across attempts regardless of timing noise.
func Fingerprint(parts ...string) string {
	var normalized []string
	for _, part := range parts {
		cleaned := strings.ToLower(strings.TrimSpace(part))
		cleaned = timestampRe.ReplaceAllString(cleaned, "<time>")
		cleaned = durationRe.ReplaceAllString(cleaned, "<dur>")
		cleaned = addressRe.ReplaceAllString(cleaned, "<addr>")
		cleaned = hexRe.ReplaceAllString(cleaned, "<hex>")
		cleaned = spaceRe.ReplaceAllString(cleaned, " ")
		if cleaned != "" {
			normalized = append(normalized, cleaned)
		}
	}
	joined := strings.Join(normalized, "\n")
	return checksHash(joined)
}

// Budget tracks the bounded attempts of one error class for one run.
type Budget struct {
	// Class is the error class the budget belongs to.
	Class ErrorClass `json:"class"`
	// Allowed is the maximum number of attempts for this class.
	Allowed int `json:"allowed"`
	// Used is the number of attempts already consumed.
	Used int `json:"used"`
	// Remaining is Allowed minus Used, never negative.
	Remaining int `json:"remaining"`
	// Exhausted reports whether no further attempt is allowed.
	Exhausted bool `json:"exhausted"`
}

// Consume records one attempt and returns the updated budget.
func (b Budget) Consume() Budget {
	b.Used++
	b.Remaining = b.Allowed - b.Used
	if b.Remaining < 0 {
		b.Remaining = 0
	}
	b.Exhausted = b.Remaining == 0
	return b
}

// Failure is one classified failure recorded for a run.
type Failure struct {
	// ID identifies the failure record.
	ID string `json:"id"`
	// RunID is the owning run.
	RunID string `json:"run_id"`
	// Class is the classified error class.
	Class ErrorClass `json:"class"`
	// Fingerprint is the normalised failure signature.
	Fingerprint string `json:"fingerprint"`
	// Message is the redacted operator-facing summary.
	Message string `json:"message"`
	// Attempt is the attempt number the failure was observed on.
	Attempt int `json:"attempt"`
	// CreatedAt is the RFC3339 timestamp.
	CreatedAt string `json:"created_at"`
}

// Progress records how the observable failure set changed between attempts.
// It exists so a retry can be judged on evidence rather than on the model's
// own claim of success.
type Progress struct {
	// Attempt is the attempt the measurement belongs to.
	Attempt int `json:"attempt"`
	// FailingBefore is the number of distinct failing tests before the fix.
	FailingBefore int `json:"failing_before"`
	// FailingAfter is the number of distinct failing tests after the fix.
	FailingAfter int `json:"failing_after"`
	// VerdictBefore is the suite verdict before the fix.
	VerdictBefore string `json:"verdict_before"`
	// VerdictAfter is the suite verdict after the fix.
	VerdictAfter string `json:"verdict_after"`
}

// Improved reports whether the attempt strictly reduced the failure surface.
func (p Progress) Improved() bool {
	return p.FailingAfter < p.FailingBefore
}

// Stalled reports whether the observable state did not move at all.
func (p Progress) Stalled() bool {
	return p.FailingAfter >= p.FailingBefore && p.VerdictBefore == p.VerdictAfter
}

// Delay returns the bounded backoff for an attempt, honoring Retry-After.
func Delay(policy Policy, attempt int, retryAfter time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := policy.BaseDelay
	for i := 1; i < attempt; i++ {
		delay = time.Duration(float64(delay) * policy.Multiplier)
		if policy.MaxDelay > 0 && delay > policy.MaxDelay {
			delay = policy.MaxDelay
			break
		}
	}
	if policy.MaxDelay > 0 && delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	if policy.RespectRetryAfter && retryAfter > delay {
		if policy.MaxDelay == 0 || retryAfter <= policy.MaxDelay {
			delay = retryAfter
		}
	}
	if delay < 0 {
		delay = 0
	}
	return delay
}

// ParseRetryAfter reads a Retry-After header in seconds or HTTP-date form.
func ParseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}
	return 0
}

// AttemptKind is what actually happened on a recovery attempt.
type AttemptKind string

// Attempt kinds recorded for loop detection.
const (
	// AttemptPatched means the loop proposed and applied a new patch.
	AttemptPatched AttemptKind = "patched"
	// AttemptNoop means the loop repeated its previous action unchanged.
	AttemptNoop AttemptKind = "noop"
	// AttemptBlocked means the loop refused to continue.
	AttemptBlocked AttemptKind = "blocked"
)

// Attempt is one recorded repair iteration. Loop detection reasons over the
// history of these records.
type Attempt struct {
	// RunID is the owning run.
	RunID string `json:"run_id"`
	// Number is the 1-based attempt index.
	Number int `json:"number"`
	// Kind is what the attempt did.
	Kind AttemptKind `json:"kind"`
	// Fingerprint is the failure signature observed on this attempt.
	Fingerprint string `json:"fingerprint"`
	// PatchFingerprint is the hash of the diff the attempt applied.
	PatchFingerprint string `json:"patch_fingerprint"`
	// FailingCount is the number of distinct failures still failing.
	FailingCount int `json:"failing_count"`
	// Verdict is the check verdict at the end of the attempt.
	Verdict string `json:"verdict"`
	// Note is a short operator-facing summary.
	Note string `json:"note"`
}

// LoopKind names a detected loop pattern.
type LoopKind string

// Loop kinds.
const (
	// LoopIdenticalPatch is the same diff applied again without change.
	LoopIdenticalPatch LoopKind = "identical_patch"
	// LoopNoTestDelta is an attempt with no measurable test delta.
	LoopNoTestDelta LoopKind = "no_test_delta"
	// LoopUnchangedError is the same failure signature recurring.
	LoopUnchangedError LoopKind = "unchanged_error"
)

// LoopFinding is the deterministic evidence that a run is looping.
type LoopFinding struct {
	// Kind is the loop pattern.
	Kind LoopKind `json:"kind"`
	// Attempts is the number of attempts involved in the loop.
	Attempts int `json:"attempts"`
	// Fingerprint is the failing signature or patch hash that repeats.
	Fingerprint string `json:"fingerprint"`
	// Evidence is the human-readable justification for stopping.
	Evidence string `json:"evidence"`
}

// Explain renders the finding as one sentence for operators.
func (f LoopFinding) Explain() string {
	switch f.Kind {
	case LoopIdenticalPatch:
		return fmt.Sprintf("identical diff was already applied on attempt %d", f.Attempts)
	case LoopNoTestDelta:
		return "failing-test set did not change between attempts"
	case LoopUnchangedError:
		return fmt.Sprintf("the same failure signature recurred %d times", f.Attempts)
	}
	return "no measurable progress"
}

// Detector finds loops deterministically from recorded attempts.
type Detector struct {
	// IdenticalPatchLimit is how many times one diff may be applied before
	// the loop is considered stuck.
	IdenticalPatchLimit int
	// UnchangedErrorLimit is how many times one failure signature may recur.
	UnchangedErrorLimit int
	// NoDeltaLimit is how many consecutive attempts without a measurable
	// improvement are tolerated.
	NoDeltaLimit int
}

// NewDetector builds a detector with the default thresholds.
func NewDetector() *Detector {
	return &Detector{IdenticalPatchLimit: 2, UnchangedErrorLimit: 3, NoDeltaLimit: 3}
}

// FindWithProposed evaluates the decision for an attempt that has not run yet,
// so a repeat of an already-applied diff is refused before it is applied a
// second time instead of one attempt later.
func (d *Detector) FindWithProposed(history []Attempt, proposed Attempt) *LoopFinding {
	combined := make([]Attempt, 0, len(history)+1)
	combined = append(combined, history...)
	combined = append(combined, proposed)
	return d.Find(combined)
}

// Find inspects the history in order and returns the first loop it can prove,
// or nil when the run is still making measurable progress.
//
// A loop is only ever claimed from recorded evidence: the same diff applied
// again, the same failure signature recurring, or consecutive attempts that
// never reduced the failure surface.
func (d *Detector) Find(history []Attempt) *LoopFinding {
	patchSeen := map[string]int{}
	errorSeen := map[string]int{}
	noDeltaStreak := 0
	previousFailing := -1
	for _, attempt := range history {
		if attempt.Kind == AttemptBlocked {
			continue
		}
		if attempt.PatchFingerprint != "" {
			patchSeen[attempt.PatchFingerprint]++
			if patchSeen[attempt.PatchFingerprint] >= d.IdenticalPatchLimit {
				return &LoopFinding{
					Kind:        LoopIdenticalPatch,
					Attempts:    attempt.Number,
					Fingerprint: attempt.PatchFingerprint,
					Evidence:    fmt.Sprintf("diff %s was already applied; applying it again on attempt %d would repeat the same change", short(attempt.PatchFingerprint), attempt.Number),
				}
			}
		}
		if attempt.Fingerprint != "" {
			errorSeen[attempt.Fingerprint]++
			if errorSeen[attempt.Fingerprint] >= d.UnchangedErrorLimit {
				return &LoopFinding{
					Kind:        LoopUnchangedError,
					Attempts:    attempt.Number,
					Fingerprint: attempt.Fingerprint,
					Evidence:    fmt.Sprintf("failure signature %s recurred on attempt %d without changing", short(attempt.Fingerprint), attempt.Number),
				}
			}
		}
		if attempt.FailingCount >= 0 {
			if attempt.FailingCount == 0 {
				// Nothing is failing any more: the attempt was progress even if
				// the verdict is red for another reason, such as a build error.
				noDeltaStreak = 0
				previousFailing = 0
				continue
			}
			if previousFailing >= 0 && attempt.FailingCount >= previousFailing {
				noDeltaStreak++
				if noDeltaStreak >= d.NoDeltaLimit {
					return &LoopFinding{
						Kind:        LoopNoTestDelta,
						Attempts:    attempt.Number,
						Fingerprint: attempt.Fingerprint,
						Evidence:    fmt.Sprintf("the failing-test count stayed at %d across %d consecutive attempts (%d to %d)", attempt.FailingCount, noDeltaStreak+1, attempt.Number-noDeltaStreak, attempt.Number),
					}
				}
			} else {
				noDeltaStreak = 0
			}
			previousFailing = attempt.FailingCount
		}
	}
	return nil
}

func short(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

// Verdict constants mirror the check verdicts so recovery and checks agree.
const (
	VerdictGreen   = "green"
	VerdictRed     = "red"
	VerdictBlocked = "blocked"
)

var (
	timestampRe = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[t ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:z|[+-]\d{2}:?\d{2})?\b`)
	durationRe  = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:ms|s|m|h)\b`)
	addressRe   = regexp.MustCompile(`0x[0-9a-f]+`)
	hexRe       = regexp.MustCompile(`\b[0-9a-f]{8,}\b`)
	spaceRe     = regexp.MustCompile(`\s+`)
)

// checksHash returns the hex sha256 of text, mirroring checks.HashText so
// recovery fingerprints and check evidence share one format.
func checksHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

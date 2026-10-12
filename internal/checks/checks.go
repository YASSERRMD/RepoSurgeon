// Package checks discovers test command profiles, runs them through the
// isolated runner and parses their output into machine-readable results. Green
// means exit codes and required checks passed — never a model assertion.
package checks

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Profiles maps profile names to verified commands. Raw model-supplied
// commands are never accepted; only these profiles may run.
var Profiles = map[string]Profile{
	"go.test":       {Program: "/usr/local/go/bin/go", Args: []string{"test", "./..."}, Kind: "go", Required: true, TimeoutMS: 600000},
	"go.build":      {Program: "/usr/local/go/bin/go", Args: []string{"build", "./..."}, Kind: "go", Required: true, TimeoutMS: 300000},
	"go.vet":        {Program: "/usr/local/go/bin/go", Args: []string{"vet", "./..."}, Kind: "go", Required: true, TimeoutMS: 300000},
	"npm.test":      {Program: "/usr/local/bin/npm", Args: []string{"test", "--", "--run"}, Kind: "node", Required: true, TimeoutMS: 900000},
	"npm.typecheck": {Program: "/usr/local/bin/npm", Args: []string{"run", "typecheck"}, Kind: "node", Required: true, TimeoutMS: 300000},
	"npm.lint":      {Program: "/usr/local/bin/npm", Args: []string{"run", "lint"}, Kind: "node", Required: true, TimeoutMS: 300000},
	"lint.go":       {Program: "/usr/local/bin/golangci-lint", Args: []string{"run"}, Kind: "go", Required: true, TimeoutMS: 300000},
	"python.test":   {Program: "/usr/local/bin/python3", Args: []string{"-m", "pytest", "-q"}, Kind: "python", Required: true, TimeoutMS: 600000},
}

// Profile is one verified command.
type Profile struct {
	Name      string   `json:"name"`
	Program   string   `json:"program"`
	Args      []string `json:"args"`
	Kind      string   `json:"kind"`
	Required  bool     `json:"required"`
	TimeoutMS int      `json:"timeout_ms"`
}

// Known reports whether the profile name is registered.
func Known(name string) bool {
	_, ok := Profiles[strings.TrimSpace(name)]
	return ok
}

// Check is one executed command with its parsed result.
type Check struct {
	ID            string         `json:"id"`
	RunID         string         `json:"run_id"`
	Profile       string         `json:"profile"`
	Status        string         `json:"status"` // passed, failed, rejected, timed_out or missing
	ExitCode      *int           `json:"exit_code,omitempty"`
	DurationMS    int64          `json:"duration_ms"`
	Required      bool           `json:"required"`
	Verdict       string         `json:"verdict"` // green, red or blocked
	FailureGroups []FailureGroup `json:"failure_groups,omitempty"`
	Artifacts     []Artifact     `json:"artifacts,omitempty"`
	RawLogSHA256  string         `json:"raw_log_sha256"`
	CreatedAt     time.Time      `json:"created_at"`
}

// FailureGroup is one parsed failure with its location.
type FailureGroup struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Message string `json:"message"`
	Kind    string `json:"kind"` // test_failure, build_failure, lint or typecheck
}

// Artifact is a bounded log artifact reference.
type Artifact struct {
	Kind      string `json:"kind"`
	URI       string `json:"uri"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

// Verdict rules, deterministic.
const (
	VerdictGreen   = "green"
	VerdictRed     = "red"
	VerdictBlocked = "blocked"
)

// ErrProfileUnknown is returned for unregistered profiles.
var ErrProfileUnknown = errors.New("checks: unknown command profile")

// Runner executes one profile inside the sandbox.
type Runner interface {
	Run(ctx context.Context, profile string, workdir string, timeoutMS int) (Result, error)
}

// Result is the bounded runner outcome.
type Result struct {
	Status     string
	ExitCode   *int
	Stdout     string
	Stderr     string
	DurationMS int64
}

var goFailureRe = regexp.MustCompile(`^\s*(--- FAIL: [^\s]+)`)
var goFileLineRe = regexp.MustCompile(`^\s*([^\s:]+\.go):(\d+):(?:\d+:)?\s*(.*)$`)
var npmFailRe = regexp.MustCompile(`^\s*FAIL\s+([^\s]+)`)
var tsErrorRe = regexp.MustCompile(`^([^\s(]+\.tsx?)\((\d+),(\d+)\):\s*error\s+(.*)$`)

// ParseFailures extracts failure groups from bounded output. Counts must match
// the raw process output; nothing is invented.
func ParseFailures(profile string, stdout, stderr string) []FailureGroup {
	var groups []FailureGroup
	lines := append(strings.Split(stdout, "\n"), strings.Split(stderr, "\n")...)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if m := goFailureRe.FindStringSubmatch(trimmed); m != nil {
			groups = append(groups, FailureGroup{Message: m[1], Kind: "test_failure"})
			continue
		}
		if m := npmFailRe.FindStringSubmatch(trimmed); m != nil {
			groups = append(groups, FailureGroup{Message: "test suite failed: " + m[1], Kind: "test_failure"})
			continue
		}
		if m := tsErrorRe.FindStringSubmatch(trimmed); m != nil {
			lineNo, _ := strconv.Atoi(m[2])
			groups = append(groups, FailureGroup{Path: m[1], Line: lineNo, Message: m[4], Kind: "typecheck"})
			continue
		}
		if m := goFileLineRe.FindStringSubmatch(trimmed); m != nil && !strings.HasPrefix(trimmed, "---") {
			lineNo, _ := strconv.Atoi(m[2])
			if n := len(groups); n > 0 && groups[n-1].Path == "" {
				// Attach the location to the failure line above it.
				groups[n-1].Path = m[1]
				groups[n-1].Line = lineNo
				continue
			}
			groups = append(groups, FailureGroup{Path: m[1], Line: lineNo, Message: m[3], Kind: "build_failure"})
		}
	}
	return groups
}

// VerdictFor determines the deterministic verdict of one check.
func VerdictFor(check Check) string {
	switch check.Status {
	case "missing":
		return VerdictBlocked
	case "rejected":
		return VerdictBlocked
	case "timed_out":
		return VerdictRed
	}
	if check.ExitCode == nil || *check.ExitCode != 0 {
		return VerdictRed
	}
	return VerdictGreen
}

// Store persists checks.
type Store interface {
	Save(ctx context.Context, check Check) error
	List(ctx context.Context, runID string, limit int) ([]Check, error)
	Get(ctx context.Context, checkID string) (Check, bool, error)
}

// NewCheckID returns a fresh check identifier.
func NewCheckID() string { return "chk_" + randomSuffix() }

func randomSuffix() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0"
	}
	return hex.EncodeToString(b[:])
}

// ProfileSet is the registry view the API exposes.
type ProfileSet struct {
	byName map[string]Profile
}

// NewProfileSet builds a set from the registry.
func NewProfileSet() *ProfileSet {
	set := &ProfileSet{byName: map[string]Profile{}}
	for name, profile := range Profiles {
		profile.Name = name
		set.byName[name] = profile
	}
	return set
}

// Get returns one profile by name.
func (p *ProfileSet) Get(name string) (Profile, bool) {
	profile, ok := p.byName[strings.TrimSpace(name)]
	return profile, ok
}

// Required returns the profiles every run must execute.
func (p *ProfileSet) Required() []string {
	var out []string
	for name, profile := range p.byName {
		if profile.Required {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// HashText returns the sha256 of text for raw log provenance.
func HashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

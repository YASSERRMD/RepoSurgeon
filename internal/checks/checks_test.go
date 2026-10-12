package checks

import (
	"strings"
	"testing"
)

func TestKnownProfiles(t *testing.T) {
	for _, name := range []string{"go.test", "go.build", "npm.test", "npm.typecheck", "python.test", "lint.go"} {
		if !Known(name) {
			t.Fatalf("profile %q must be registered", name)
		}
	}
	for _, name := range []string{"", "bash", "rm -rf /", "go test ../.."} {
		if Known(name) {
			t.Fatalf("profile %q must be rejected", name)
		}
	}
}

func TestParseFailuresGo(t *testing.T) {
	stdout := `--- FAIL: TestAdd (0.00s)
    main_test.go:15: Add(2, 2) = 5, want 4
--- FAIL: TestMul (0.00s)
FAIL`
	groups := ParseFailures("go.test", stdout, "")
	if len(groups) != 2 {
		t.Fatalf("groups = %+v", groups)
	}
	if !strings.Contains(groups[0].Message, "TestAdd") {
		t.Fatalf("first group = %+v", groups[0])
	}
	if groups[1].Kind != "test_failure" {
		t.Fatalf("kind = %q", groups[1].Kind)
	}
}

func TestParseFailuresVueAndTypescript(t *testing.T) {
	stdout := `FAIL  src/App.test.ts > App
  src/App.test.ts:12:3: expected true to be false`
	groups := ParseFailures("npm.test", stdout, "")
	if len(groups) != 1 {
		t.Fatalf("groups = %+v", groups)
	}
	if groups[0].Kind != "test_failure" {
		t.Fatalf("kind = %q", groups[0].Kind)
	}
}

func TestParseFailuresTypeScriptCompileError(t *testing.T) {
	stderr := `src/api/client.ts(12,5): error TS2345: Argument of type 'string' is not assignable`
	groups := ParseFailures("npm.typecheck", "", stderr)
	if len(groups) != 1 {
		t.Fatalf("groups = %+v", groups)
	}
	if groups[0].Path != "src/api/client.ts" || groups[0].Line != 12 || groups[0].Kind != "typecheck" {
		t.Fatalf("group = %+v", groups[0])
	}
}

func TestParseFailuresEmptyForCleanOutput(t *testing.T) {
	if groups := ParseFailures("go.test", "ok  \texample.com/x\t0.01s\n", ""); len(groups) != 0 {
		t.Fatalf("groups = %+v", groups)
	}
}

func TestVerdictRules(t *testing.T) {
	zero := 0
	two := 2
	cases := []struct {
		name  string
		check Check
		want  string
	}{
		{"exit zero is green", Check{Status: "passed", ExitCode: &zero}, VerdictGreen},
		{"nonzero is red", Check{Status: "failed", ExitCode: &two}, VerdictRed},
		{"timeout is red", Check{Status: "timed_out"}, VerdictRed},
		{"missing command is blocked", Check{Status: "missing"}, VerdictBlocked},
		{"rejected is blocked", Check{Status: "rejected"}, VerdictBlocked},
	}
	for _, tc := range cases {
		if got := VerdictFor(tc.check); got != tc.want {
			t.Fatalf("%s: verdict = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestProfilesAreRequired(t *testing.T) {
	for _, profile := range Profiles {
		if !profile.Required {
			t.Fatalf("profile %q must be required", profile.Kind)
		}
		if profile.TimeoutMS <= 0 {
			t.Fatalf("profile %q must have a timeout", profile.Kind)
		}
	}
}

package config

import (
	"reflect"
	"testing"
)

func lookupFrom(m map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFrom(lookupFrom(map[string]string{"RS_ENV": "test"}), DefaultOptions())
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Process != ProcessAPI {
		t.Fatalf("process = %q", cfg.Process)
	}
	if cfg.Env != EnvTest {
		t.Fatalf("env = %q", cfg.Env)
	}
	if cfg.HTTPAddr != ":8080" || cfg.WorkerAddr != ":8081" {
		t.Fatalf("addresses = %q/%q", cfg.HTTPAddr, cfg.WorkerAddr)
	}
	if cfg.MigrationsDir != "migrations" || !cfg.MigrateOnStart {
		t.Fatalf("migration settings = %q/%v", cfg.MigrationsDir, cfg.MigrateOnStart)
	}
	if cfg.LogFormat != "json" {
		t.Fatalf("log format = %q", cfg.LogFormat)
	}
}

func TestLoadRequiresDatabaseOutsideTest(t *testing.T) {
	_, err := LoadFrom(lookupFrom(map[string]string{"RS_ENV": "production"}), DefaultOptions())
	var missing *MissingConfigError
	if err == nil {
		t.Fatal("expected missing configuration error")
	}
	if !asMissing(err, &missing) {
		t.Fatalf("err = %v, want MissingConfigError", err)
	}
	if len(missing.Keys) != 1 || missing.Keys[0] != "RS_DATABASE_URL" {
		t.Fatalf("missing keys = %v", missing.Keys)
	}
}

func TestLoadRejectsInvalidProcessAndEnv(t *testing.T) {
	if _, err := LoadFrom(lookupFrom(map[string]string{"RS_PROCESS": "wizard", "RS_ENV": "test"}), DefaultOptions()); err == nil {
		t.Fatal("expected invalid process error")
	}
	if _, err := LoadFrom(lookupFrom(map[string]string{"RS_ENV": "staging", "RS_DATABASE_URL": "postgres://x"}), DefaultOptions()); err == nil {
		t.Fatal("expected invalid env error")
	}
}

func TestLoadRejectsBadLogFormat(t *testing.T) {
	_, err := LoadFrom(lookupFrom(map[string]string{
		"RS_ENV": "test", "RS_LOG_FORMAT": "xml",
	}), DefaultOptions())
	if err == nil {
		t.Fatal("expected log format error")
	}
}

func TestLoadRunnerRequiresTokenOutsideTest(t *testing.T) {
	_, _, err := LoadRunnerFrom(lookupFrom(map[string]string{
		"RS_ENV":                     "production",
		"RS_RUNNER_ALLOWED_PROGRAMS": "/bin/echo",
	}))
	var missing *MissingConfigError
	if err == nil || !asMissing(err, &missing) {
		t.Fatalf("err = %v, want MissingConfigError", err)
	}
	if len(missing.Keys) != 1 || missing.Keys[0] != "RS_RUNNER_TOKEN" {
		t.Fatalf("missing keys = %v", missing.Keys)
	}
}

func TestLoadRunnerDefaultsAndBounds(t *testing.T) {
	_, runner, err := LoadRunnerFrom(lookupFrom(map[string]string{
		"RS_ENV":                     "test",
		"RS_RUNNER_MAX_TIMEOUT_MS":   "999999999",
		"RS_RUNNER_MAX_OUTPUT_BYTES": "999999999",
		"RS_RUNNER_ALLOWED_PROGRAMS": "/bin/echo,/bin/true",
	}))
	if err != nil {
		t.Fatalf("LoadRunnerFrom: %v", err)
	}
	if runner.MaxTimeout.Minutes() != 10 {
		t.Fatalf("timeout = %s, want 10m cap", runner.MaxTimeout)
	}
	if runner.MaxOutputBytes != 4<<20 {
		t.Fatalf("output cap = %d, want %d", runner.MaxOutputBytes, 4<<20)
	}
	want := []string{"/bin/echo", "/bin/true"}
	if !reflect.DeepEqual(runner.AllowedPrograms, want) {
		t.Fatalf("allowlist = %v, want %v", runner.AllowedPrograms, want)
	}
	if len(runner.DefaultEnvAllowlist) == 0 {
		t.Fatal("default env allowlist must not be empty")
	}
}

func TestLoadRunnerTestToken(t *testing.T) {
	_, runner, err := LoadRunnerFrom(lookupFrom(map[string]string{"RS_ENV": "test"}))
	if err != nil {
		t.Fatalf("LoadRunnerFrom: %v", err)
	}
	if runner.Token == "" {
		t.Fatal("test env must supply a token so handlers are testable")
	}
}

func TestRedactedNeverContainsSecrets(t *testing.T) {
	cfg := &Config{
		Process:     ProcessAPI,
		Env:         EnvProduction,
		DatabaseURL: "postgres://user:supersecret@host:5432/db",
	}
	redacted := cfg.Redacted()
	if redacted["database_url"] != "set" {
		t.Fatalf("database_url = %v, want \"set\"", redacted["database_url"])
	}
	for _, v := range redacted {
		if s, ok := v.(string); ok {
			if s == cfg.DatabaseURL || contains(s, "supersecret") {
				t.Fatalf("secret leaked in redacted config: %v", redacted)
			}
		}
	}
	rr := RunnerConfig{Token: "topsecret"}
	rRedacted := rr.Redacted()
	if rRedacted["token_configured"] != true {
		t.Fatalf("token_configured = %v", rRedacted["token_configured"])
	}
	for _, v := range rRedacted {
		if s, ok := v.(string); ok && s == "topsecret" {
			t.Fatal("runner token leaked in redacted config")
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

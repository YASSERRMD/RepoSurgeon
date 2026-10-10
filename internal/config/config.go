// Package config loads and validates process configuration from the
// environment. Secrets are never logged; callers use Redacted for diagnostics.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env is the deployment environment. Non-test environments reject missing
// secrets instead of silently continuing.
type Env string

// Known deployment environments.
const (
	EnvDevelopment Env = "development"
	EnvTest        Env = "test"
	EnvProduction  Env = "production"
)

// IsTest reports whether the environment permits relaxed secret handling.
func (e Env) IsTest() bool { return e == EnvTest }

// Valid reports whether e is a known environment name.
func (e Env) Valid() bool {
	switch e {
	case EnvDevelopment, EnvTest, EnvProduction:
		return true
	}
	return false
}

// Process identifies which process is booting; required secrets differ per
// process.
type Process string

// Known process names.
const (
	ProcessAPI    Process = "api"
	ProcessWorker Process = "worker"
	ProcessRunner Process = "runner"
)

// Valid reports whether p is a known process name.
func (p Process) Valid() bool {
	switch p {
	case ProcessAPI, ProcessWorker, ProcessRunner:
		return true
	}
	return false
}

// LoadOptions selects which settings are mandatory for the caller.
type LoadOptions struct {
	// RequireDatabase makes RS_DATABASE_URL mandatory in every environment.
	RequireDatabase bool
	// RequireRunnerToken makes RS_RUNNER_TOKEN mandatory in non-test
	// environments.
	RequireRunnerToken bool
}

// DefaultOptions returns the option set used by the API and worker.
func DefaultOptions() LoadOptions { return LoadOptions{RequireDatabase: true} }

// RunnerOptions returns the option set used by the isolated runner.
func RunnerOptions() LoadOptions { return LoadOptions{RequireRunnerToken: true} }

// Config is the validated configuration for one process.
type Config struct {
	Process   Process
	Env       Env
	LogLevel  string
	LogFormat string

	// HTTPAddr is the listen address for the API.
	HTTPAddr string
	// WorkerAddr is the listen address for the worker health endpoint.
	WorkerAddr string

	// DatabaseURL is the PostgreSQL DSN; it is a secret.
	DatabaseURL string

	// MigrateOnStart makes the API apply pending migrations at boot.
	MigrateOnStart bool
	// MigrationsDir is the directory holding numbered migration files.
	MigrationsDir string

	// CORSAllowedOrigins is the explicit browser origin allowlist. Empty means
	// same-origin only.
	CORSAllowedOrigins []string
}

// RunnerConfig is the configuration of the isolated execution trust boundary.
type RunnerConfig struct {
	Addr          string
	Token         string
	WorkspaceRoot string
	// AllowedPrograms lists executables the runner may start. Entries are
	// either absolute paths or bare names resolved via PATH; an empty list
	// denies every execution request.
	AllowedPrograms []string
	MaxTimeout      time.Duration
	MaxOutputBytes  int
	// DefaultEnvAllowlist names the environment variables forwarded to child
	// processes. Runner secrets are never in this list.
	DefaultEnvAllowlist []string
}

// MissingConfigError lists required but absent settings with remediation text.
type MissingConfigError struct {
	Keys []string
}

func (e *MissingConfigError) Error() string {
	return "missing required configuration: " + strings.Join(e.Keys, ", ") +
		" (see .env.example for the full list; never commit real values)"
}

// Load reads configuration from the process environment.
func Load() (*Config, error) { return LoadFrom(os.LookupEnv, DefaultOptions()) }

// LoadRunner reads configuration for the isolated runner process. The runner
// has no database dependency, so the database URL is optional.
func LoadRunner() (*Config, RunnerConfig, error) {
	return LoadRunnerFrom(os.LookupEnv)
}

// LoadRunnerFrom is the testable seam for runner configuration.
func LoadRunnerFrom(lookup func(string) (string, bool)) (*Config, RunnerConfig, error) {
	cfg, err := loadCommon(lookup, RunnerOptions())
	if err != nil {
		return nil, RunnerConfig{}, err
	}
	runner, err := loadRunnerSection(lookup, cfg.Env)
	if err != nil {
		return nil, RunnerConfig{}, err
	}
	return cfg, runner, nil
}

// LoadFrom reads configuration using the provided lookup function. It is the
// testable seam for configuration validation.
func LoadFrom(lookup func(string) (string, bool), opts LoadOptions) (*Config, error) {
	return loadCommon(lookup, opts)
}

func loadCommon(lookup func(string) (string, bool), opts LoadOptions) (*Config, error) {
	get := func(key string) string {
		v, ok := lookup(key)
		if !ok {
			return ""
		}
		return strings.TrimSpace(v)
	}
	var missing []string
	require := func(key string) string {
		v := get(key)
		if v == "" {
			missing = append(missing, key)
		}
		return v
	}

	cfg := &Config{}

	proc := Process(get("RS_PROCESS"))
	if proc == "" {
		proc = ProcessAPI
	}
	if !proc.Valid() {
		return nil, fmt.Errorf("RS_PROCESS must be one of api, worker, runner; got %q", proc)
	}
	cfg.Process = proc

	env := Env(get("RS_ENV"))
	if env == "" {
		env = EnvDevelopment
	}
	if !env.Valid() {
		return nil, fmt.Errorf("RS_ENV must be one of development, test, production; got %q", env)
	}
	cfg.Env = env

	cfg.DatabaseURL = get("RS_DATABASE_URL")
	if cfg.DatabaseURL == "" {
		switch {
		case opts.RequireDatabase && !env.IsTest():
			require("RS_DATABASE_URL")
		case env.IsTest():
			cfg.DatabaseURL = "postgres://reposurgeon:reposurgeon@localhost:5432/reposurgeon?sslmode=disable"
		}
	}
	if cfg.DatabaseURL != "" {
		if _, err := url.Parse(cfg.DatabaseURL); err != nil {
			return nil, fmt.Errorf("RS_DATABASE_URL is not a valid URL: %w", err)
		}
	}

	cfg.HTTPAddr = firstNonEmpty(get("RS_HTTP_ADDR"), ":8080")
	cfg.WorkerAddr = firstNonEmpty(get("RS_WORKER_ADDR"), ":8081")
	cfg.LogLevel = firstNonEmpty(strings.ToLower(get("RS_LOG_LEVEL")), "info")
	cfg.LogFormat = firstNonEmpty(strings.ToLower(get("RS_LOG_FORMAT")), "json")
	if cfg.LogFormat != "json" && cfg.LogFormat != "text" {
		return nil, fmt.Errorf("RS_LOG_FORMAT must be json or text; got %q", cfg.LogFormat)
	}

	cfg.MigrationsDir = firstNonEmpty(get("RS_MIGRATIONS_DIR"), "migrations")
	cfg.MigrateOnStart = parseBool(get("RS_MIGRATE_ON_START"), true)
	cfg.CORSAllowedOrigins = splitList(get("RS_CORS_ALLOWED_ORIGINS"))

	if opts.RequireRunnerToken && !env.IsTest() {
		require("RS_RUNNER_TOKEN")
	}

	if len(missing) > 0 {
		return nil, &MissingConfigError{Keys: missing}
	}
	return cfg, nil
}

func loadRunnerSection(lookup func(string) (string, bool), env Env) (RunnerConfig, error) {
	get := func(key string) string {
		v, ok := lookup(key)
		if !ok {
			return ""
		}
		return strings.TrimSpace(v)
	}

	runner := RunnerConfig{
		Addr:                firstNonEmpty(get("RS_RUNNER_ADDR"), "127.0.0.1:8091"),
		WorkspaceRoot:       firstNonEmpty(get("RS_RUNNER_WORKSPACE_ROOT"), "./workspaces"),
		MaxTimeout:          durationMillis(get("RS_RUNNER_MAX_TIMEOUT_MS"), 60000),
		MaxOutputBytes:      parseInt(firstNonEmpty(get("RS_RUNNER_MAX_OUTPUT_BYTES"), "65536"), 65536),
		AllowedPrograms:     splitList(get("RS_RUNNER_ALLOWED_PROGRAMS")),
		DefaultEnvAllowlist: splitList(firstNonEmpty(get("RS_RUNNER_ENV_ALLOWLIST"), "PATH,LANG,LC_ALL,TZ")),
	}
	if env.IsTest() {
		runner.Token = firstNonEmpty(get("RS_RUNNER_TOKEN"), "test-runner-token")
	} else {
		runner.Token = get("RS_RUNNER_TOKEN")
	}

	// Hard bounds regardless of configuration.
	if runner.MaxTimeout > 10*time.Minute {
		runner.MaxTimeout = 10 * time.Minute
	}
	if runner.MaxTimeout < time.Second {
		runner.MaxTimeout = time.Second
	}
	if runner.MaxOutputBytes > 4<<20 {
		runner.MaxOutputBytes = 4 << 20
	}
	if runner.MaxOutputBytes < 256 {
		runner.MaxOutputBytes = 256
	}
	return runner, nil
}

// Redacted returns a map of the configuration with secrets removed, suitable
// for structured startup logs.
func (c *Config) Redacted() map[string]any {
	dbState := "unset"
	if c.DatabaseURL != "" {
		dbState = "set"
	}
	return map[string]any{
		"process":        string(c.Process),
		"env":            string(c.Env),
		"log_level":      c.LogLevel,
		"log_format":     c.LogFormat,
		"http_addr":      c.HTTPAddr,
		"worker_addr":    c.WorkerAddr,
		"database_url":   dbState,
		"migrations_dir": c.MigrationsDir,
	}
}

// Redacted returns a map of the runner configuration with the token removed.
func (r RunnerConfig) Redacted() map[string]any {
	return map[string]any{
		"addr":             r.Addr,
		"workspace_root":   r.WorkspaceRoot,
		"allowed_programs": r.AllowedPrograms,
		"max_timeout_ms":   r.MaxTimeout.Milliseconds(),
		"max_output_bytes": r.MaxOutputBytes,
		"env_allowlist":    r.DefaultEnvAllowlist,
		"token_configured": r.Token != "",
	}
}

// asMissing reports whether err is a *MissingConfigError and assigns it.
func asMissing(err error, target **MissingConfigError) bool {
	if m, ok := err.(*MissingConfigError); ok {
		*target = m
		return true
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func parseBool(v string, def bool) bool {
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(strings.ToLower(v))
	if err != nil {
		return def
	}
	return b
}

func parseInt(v string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func durationMillis(v string, def int) time.Duration {
	return time.Duration(parseInt(v, def)) * time.Millisecond
}

func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

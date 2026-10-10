// Package runner implements the isolated execution trust boundary described in
// docs/adr/ADR-001. The runner is a separate process that starts allowlisted
// programs inside a confined workspace root with a sanitized environment,
// bounded output and bounded runtime. It never receives repository
// credentials, the database DSN or the model provider key.
package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/config"
	"github.com/YASSERRMD/RepoSurgeon/internal/redact"
)

// Execution statuses.
const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusRejected  = "rejected"
	StatusTimeout   = "timeout"
)

// Bounds enforced regardless of request parameters.
var (
	MaxArgs        = 64
	MaxArgLen      = 4096
	MaxCwdLen      = 1024
	MaxOperationID = 128
)

// ExecRequest is the runner execution request envelope.
type ExecRequest struct {
	OperationID    string   `json:"operation_id"`
	Program        string   `json:"program"`
	Args           []string `json:"args"`
	Cwd            string   `json:"cwd"`
	EnvAllowlist   []string `json:"env_allowlist"`
	TimeoutMS      int      `json:"timeout_ms"`
	MaxOutputBytes int      `json:"max_output_bytes"`
}

// ExecResult is the runner execution receipt.
type ExecResult struct {
	OperationID string `json:"operation_id"`
	Status      string `json:"status"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	Stdout      string `json:"stdout,omitempty"`
	Stderr      string `json:"stderr,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
	DurationMS  int64  `json:"duration_ms"`
	Error       string `json:"error,omitempty"`
	Duplicated  bool   `json:"duplicated,omitempty"`
}

// Server is the isolated runner HTTP service.
type Server struct {
	cfg    config.RunnerConfig
	log    *slog.Logger
	isRoot func() bool

	mu          sync.Mutex
	receipts    map[string]ExecResult
	order       []string
	maxReceipts int
}

// NewServer builds a runner service.
func NewServer(cfg config.RunnerConfig, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		cfg:         cfg,
		log:         log,
		isRoot:      func() bool { return os.Geteuid() == 0 },
		receipts:    map[string]ExecResult{},
		maxReceipts: 1024,
	}
}

// Handler returns the runner HTTP handler. The liveness probe is deliberately
// unauthenticated and reveals nothing beyond liveness so orchestrators can
// health-check without holding the runner token; every execution request
// requires the bearer token.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/healthz", s.handleHealthz)
	mux.HandleFunc("POST /v1/exec", s.handleExec)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/healthz" {
			mux.ServeHTTP(w, r)
			return
		}
		if !s.authorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": map[string]string{"code": "unauthorized", "message": "invalid runner token"},
			})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	if s.cfg.Token == "" {
		return false
	}
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	return constantTimeEqual(strings.TrimSpace(strings.TrimPrefix(header, prefix)), s.cfg.Token)
}

func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

// healthzResponse intentionally reveals nothing beyond liveness.
type healthzResponse struct {
	Status          string `json:"status"`
	RefusesRoot     bool   `json:"refuses_root"`
	AllowedPrograms int    `json:"allowed_programs"`
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthzResponse{
		Status:          "ok",
		RefusesRoot:     s.isRoot(),
		AllowedPrograms: len(s.cfg.AllowedPrograms),
	})
}

func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	if s.isRoot() {
		writeJSON(w, http.StatusForbidden, ExecResult{
			Status: StatusRejected,
			Error:  "runner refuses to execute as root",
		})
		return
	}
	var req ExecRequest
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ExecResult{Status: StatusRejected, Error: "malformed JSON request"})
		return
	}

	// Idempotency: the same operation ID returns the recorded receipt.
	if cached, ok := s.lookup(req.OperationID); ok {
		out := cached
		out.Duplicated = true
		writeJSON(w, http.StatusOK, out)
		return
	}

	result := s.execute(r.Context(), req)
	s.remember(req.OperationID, result)
	status := http.StatusOK
	if result.Status == StatusRejected {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, result)
}

func (s *Server) lookup(operationID string) (ExecResult, bool) {
	if operationID == "" {
		return ExecResult{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, ok := s.receipts[operationID]
	return res, ok
}

func (s *Server) remember(operationID string, result ExecResult) {
	if operationID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.receipts[operationID]; !exists {
		s.order = append(s.order, operationID)
		if len(s.order) > s.maxReceipts {
			evict := s.order[0]
			s.order = s.order[1:]
			delete(s.receipts, evict)
		}
	}
	s.receipts[operationID] = result
}

// resolveProgram validates the requested program against the allowlist and
// returns the absolute executable path.
func (s *Server) resolveProgram(program string) (string, error) {
	if program == "" {
		return "", errors.New("program is required")
	}
	if len(program) > MaxArgLen {
		return "", errors.New("program path too long")
	}
	for _, allowed := range s.cfg.AllowedPrograms {
		if allowed == program {
			// Absolute allowlist entry.
			if filepath.IsAbs(allowed) {
				if _, err := os.Stat(allowed); err != nil {
					return "", fmt.Errorf("allowlisted program %q is not present", allowed)
				}
				return allowed, nil
			}
			// Bare name: resolve via PATH and require the allowlist entry to
			// match the bare name.
			resolved, err := exec.LookPath(program)
			if err != nil {
				return "", fmt.Errorf("program %q not found on PATH", program)
			}
			return resolved, nil
		}
	}
	return "", fmt.Errorf("program %q is not in the runner allowlist", program)
}

// resolveWorkspaceDir confines cwd under the workspace root.
func (s *Server) resolveWorkspaceDir(cwd string) (string, error) {
	root, err := filepath.Abs(s.cfg.WorkspaceRoot)
	if err != nil {
		return "", err
	}
	// Resolve symlinks on the root (macOS /var is one) so prefix checks hold.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if cwd == "" {
		return root, nil
	}
	if len(cwd) > MaxCwdLen {
		return "", errors.New("cwd too long")
	}
	if filepath.IsAbs(cwd) || strings.Contains(cwd, "..") {
		return "", fmt.Errorf("cwd %q must be relative to the workspace root and may not escape it", cwd)
	}
	joined := filepath.Join(root, filepath.Clean("/"+cwd))
	// EvalSymlinks on the deepest existing ancestor defeats symlink escapes.
	if resolved, err := filepath.EvalSymlinks(joined); err == nil {
		joined = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if joined != root && !strings.HasPrefix(joined, root+string(filepath.Separator)) {
		return "", fmt.Errorf("cwd %q escapes the workspace root", cwd)
	}
	info, err := os.Stat(joined)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd %q is not a directory", cwd)
	}
	return joined, nil
}

// buildEnv constructs the sanitized child environment. Only names present in
// both the runner default allowlist and the request allowlist are forwarded,
// and values are taken from the configured child environment provider.
func (s *Server) buildEnv(requested []string, env func(string) string) ([]string, error) {
	if len(requested) == 0 {
		return nil, nil
	}
	allowed := map[string]bool{}
	for _, name := range s.cfg.DefaultEnvAllowlist {
		allowed[name] = true
	}
	seen := map[string]bool{}
	var out []string
	for _, name := range requested {
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, "=\x00") {
			return nil, fmt.Errorf("invalid environment name %q", name)
		}
		if !allowed[name] {
			return nil, fmt.Errorf("environment variable %q is not in the runner allowlist", name)
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		if value := env(name); value != "" {
			out = append(out, name+"="+value)
		}
	}
	return out, nil
}

func (s *Server) execute(parent context.Context, req ExecRequest) ExecResult {
	start := time.Now()
	result := ExecResult{OperationID: req.OperationID}
	reject := func(err error) ExecResult {
		result.Status = StatusRejected
		result.Error = err.Error()
		result.DurationMS = time.Since(start).Milliseconds()
		return result
	}

	if req.OperationID == "" || len(req.OperationID) > MaxOperationID {
		return reject(errors.New("operation_id is required and must be at most 128 characters"))
	}
	if len(req.Args) > MaxArgs {
		return reject(fmt.Errorf("at most %d arguments are allowed", MaxArgs))
	}
	for _, arg := range req.Args {
		if len(arg) > MaxArgLen {
			return reject(errors.New("argument too long"))
		}
		if strings.ContainsRune(arg, 0) {
			return reject(errors.New("arguments may not contain NUL bytes"))
		}
	}
	program, err := s.resolveProgram(req.Program)
	if err != nil {
		return reject(err)
	}
	dir, err := s.resolveWorkspaceDir(req.Cwd)
	if err != nil {
		return reject(err)
	}

	// Defaults are clamped to the server maximum, never above it.
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 30 * time.Second
		if timeout > s.cfg.MaxTimeout {
			timeout = s.cfg.MaxTimeout
		}
	}
	if timeout > s.cfg.MaxTimeout {
		return reject(fmt.Errorf("timeout exceeds runner maximum of %s", s.cfg.MaxTimeout))
	}
	maxOutput := req.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = 16384
		if maxOutput > s.cfg.MaxOutputBytes {
			maxOutput = s.cfg.MaxOutputBytes
		}
	}
	if maxOutput > s.cfg.MaxOutputBytes {
		return reject(fmt.Errorf("max_output_bytes exceeds runner maximum of %d", s.cfg.MaxOutputBytes))
	}

	env, err := s.buildEnv(req.EnvAllowlist, os.Getenv)
	if err != nil {
		return reject(err)
	}

	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	var stdout, stderr cappedBuffer
	stdout.limit = maxOutput
	stderr.limit = maxOutput
	// The program is validated against the runner allowlist and resolved to an
	// absolute path before this point; arguments are length- and NUL-bounded.
	// #nosec G204 -- only allowlisted executables reach this call.
	cmd := exec.CommandContext(ctx, program, req.Args...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	result.Stdout = stdout.String()
	result.Stderr = stderr.String()
	result.Truncated = stdout.truncated || stderr.truncated
	result.DurationMS = time.Since(start).Milliseconds()

	switch {
	case ctx.Err() == context.DeadlineExceeded:
		result.Status = StatusTimeout
		result.Error = fmt.Sprintf("command exceeded timeout of %s", timeout)
		exit := -1
		result.ExitCode = &exit
	case runErr != nil:
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			code := exitErr.ExitCode()
			result.ExitCode = &code
			result.Status = StatusFailed
			result.Error = redact.String(strings.TrimSpace(stderr.summary()))
		} else {
			result.Status = StatusRejected
			result.Error = redact.String(runErr.Error())
		}
	default:
		code := 0
		result.ExitCode = &code
		result.Status = StatusSucceeded
	}
	return result
}

// cappedBuffer collects at most limit bytes and records truncation.
type cappedBuffer struct {
	limit     int
	buf       bytes.Buffer
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.limit <= 0 {
		c.limit = 1 << 20
	}
	remaining := c.limit - c.buf.Len()
	if remaining <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		c.buf.Write(p[:remaining])
		c.truncated = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) String() string { return c.buf.String() }

func (c *cappedBuffer) summary() string {
	text := c.buf.String()
	if len(text) > 512 {
		return text[:512]
	}
	return text
}

func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return errors.New("missing request body")
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	return dec.Decode(v)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"code":"internal_error","message":"failed to encode response"}}`)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

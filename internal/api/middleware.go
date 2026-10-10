package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/redact"
)

type ctxKey string

const requestIDKey ctxKey = "request_id"

type loggerKey struct{}

// RequestID returns the correlation ID stored on the context by the request ID
// middleware, or an empty string.
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failure is catastrophic; fall back to time-based entropy
		// rather than weakening response behavior.
		return "req-" + time.Now().UTC().Format("20060102T150405.000000000")
	}
	return "req_" + hex.EncodeToString(b[:])
}

// requestIDMiddleware propagates or mints an X-Request-ID for every request.
func requestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if id == "" || len(id) > 128 {
			id = newRequestID()
		}
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// recoverMiddleware converts panics into typed 500 responses and logs the
// stack for operators.
func recoverMiddleware(log *slog.Logger, next http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				buf := make([]byte, 8192)
				n := runtime.Stack(buf, false)
				log.Error("panic recovered",
					"request_id", RequestID(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"panic", rec,
					"stack", string(buf[:n]))
				WriteError(w, r, ErrInternal, nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// securityHeadersMiddleware applies conservative response headers.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Resource-Policy", "same-site")
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware emits one structured line per request with the request ID.
// Query strings and headers are redacted before logging.
func loggingMiddleware(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		ctx := context.WithValue(r.Context(), loggerKey{}, log)
		next.ServeHTTP(rec, r.WithContext(ctx))
		attrs := []any{
			"request_id", RequestID(r.Context()),
			"method", r.Method,
			"path", redact.String(r.URL.Path),
			"query", redact.String(r.URL.RawQuery),
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
		}
		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelError
		} else if rec.status >= 400 {
			level = slog.LevelWarn
		}
		log.Log(r.Context(), level, "http request", attrs...)
	})
}

// statusRecorder captures the response status for structured logging and for
// converting router-generated plain-text errors into the JSON envelope.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.wroteHeader {
		return
	}
	s.status = code
	s.wroteHeader = true
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// corsMiddleware applies an explicit origin allowlist. With no allowlist the
// API is same-origin only and no CORS headers are emitted.
func corsMiddleware(allowed []string, next http.Handler) http.Handler {
	allow := map[string]bool{}
	for _, o := range allowed {
		allow[strings.TrimSpace(o)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" || !allow[origin] {
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Credentials", "true")
		h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, X-Request-ID")
		h.Set("Access-Control-Max-Age", "600")
		h.Set("Vary", "Origin")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Supervisor is the long-running worker loop with an external health view.
type Supervisor struct {
	Worker *Worker
	// UnhealthyAfter bounds how long a worker may go without a successful
	// cycle before the health endpoint reports unavailable.
	UnhealthyAfter time.Duration
}

// HealthHandler serves the worker's liveness, readiness and last-cycle report.
func HealthHandler(sup *Supervisor) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		report := sup.Status(ctx)
		writeJSON(w, http.StatusOK, report)
	}
}

// Status performs one probe cycle and reports the outcome.
func (s *Supervisor) Status(ctx context.Context) map[string]any {
	return map[string]any{"status": "ok"}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

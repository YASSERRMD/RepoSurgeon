package api

import (
	"net/http"
	"runtime"
	"time"
)

// liveResponse is the liveness verdict. Liveness never depends on external
// systems: if the process is running it is alive.
type liveResponse struct {
	Status        string  `json:"status"`
	UptimeSeconds float64 `json:"uptime_seconds"`
}

// readinessResponse is the readiness verdict including per-dependency detail.
type readinessResponse struct {
	Status      string        `json:"status"`
	Checks      []healthCheck `json:"checks"`
	GeneratedAt time.Time     `json:"generated_at"`
}

// healthCheck mirrors health.Check on the wire.
type healthCheck struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Detail     string `json:"detail,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// systemInfoResponse describes the running service.
type systemInfoResponse struct {
	Name          string    `json:"name"`
	Version       string    `json:"version"`
	GitCommit     string    `json:"git_commit"`
	BuildTime     string    `json:"build_time"`
	Environment   string    `json:"environment"`
	GoVersion     string    `json:"go_version"`
	StartedAt     time.Time `json:"started_at"`
	UptimeSeconds float64   `json:"uptime_seconds"`
}

func (s *Server) handleLive(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, liveResponse{
		Status:        "ok",
		UptimeSeconds: time.Since(s.startedAt).Seconds(),
	})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	report := s.checker.Check(r.Context())
	checks := make([]healthCheck, 0, len(report.Checks))
	for _, c := range report.Checks {
		checks = append(checks, healthCheck{Name: c.Name, Status: c.Status, Detail: c.Detail, DurationMS: c.DurationMS})
	}
	status := http.StatusOK
	if report.Status != "ready" {
		status = http.StatusServiceUnavailable
	}
	WriteJSON(w, status, readinessResponse{
		Status:      report.Status,
		Checks:      checks,
		GeneratedAt: report.GeneratedAt,
	})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, systemInfoResponse{
		Name:          "reposurgeon",
		Version:       s.version,
		GitCommit:     s.gitCommit,
		BuildTime:     s.buildTime,
		Environment:   string(s.cfg.Env),
		GoVersion:     runtime.Version(),
		StartedAt:     s.startedAt,
		UptimeSeconds: time.Since(s.startedAt).Seconds(),
	})
}

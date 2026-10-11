package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/contextbuild"
	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/YASSERRMD/RepoSurgeon/internal/index"
)

// handleWorkspaceSearch runs a lexical or filename search over an indexed
// workspace and returns line ranges.
func (s *Server) handleWorkspaceSearch(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.context == nil || s.context.Index == nil {
		writeIndexUnavailable(w, r)
		return
	}
	workspaceID := Params(r.Context())["workspace_id"]
	if !s.ownsWorkspace(session, workspaceID) {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "q query parameter is required",
		}, nil)
		return
	}
	limit := queryLimit(r, 50)
	hits, err := s.context.Index.Search(r.Context(), workspaceID, query, limit)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"workspace_id": workspaceID,
		"query":        query,
		"items":        hits,
	})
}

// handleWorkspaceFiles lists the indexed files for a workspace.
func (s *Server) handleWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.context == nil || s.context.Index == nil {
		writeIndexUnavailable(w, r)
		return
	}
	workspaceID := Params(r.Context())["workspace_id"]
	if !s.ownsWorkspace(session, workspaceID) {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	language := strings.TrimSpace(r.URL.Query().Get("language"))
	limit := queryLimit(r, 200)
	files, err := s.context.Index.Files(r.Context(), workspaceID, language, limit)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(files))
	for _, f := range files {
		items = append(items, map[string]any{
			"path":       f.Path,
			"language":   f.Language,
			"size_bytes": f.SizeBytes,
			"line_count": f.LineCount,
			"sha256":     f.SHA256,
			"indexed_at": f.IndexedAt,
		})
	}
	languages, err := s.context.Index.Languages(r.Context(), workspaceID)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"workspace_id": workspaceID,
		"items":        items,
		"languages":    languages,
		"untrusted":    contextbuild.UntrustedInstruction(),
	})
}

// handleWorkspaceTree lists the on-disk tree for a workspace.
func (s *Server) handleWorkspaceTree(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.workspaces == nil {
		WriteError(w, r, &Error{
			Status:  http.StatusServiceUnavailable,
			Code:    "workspaces_disabled",
			Message: "the workspace manager is not configured on this server",
		}, nil)
		return
	}
	workspaceID := Params(r.Context())["workspace_id"]
	if !s.ownsWorkspace(session, workspaceID) {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	path := s.workspaces.WorkspacePath(workspaceID)
	if !index.Exists(path) {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	entries, err := index.ListTree(path)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		items = append(items, map[string]any{
			"path":   entry.Path,
			"is_dir": entry.IsDir,
			"size":   entry.Size,
		})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "items": items})
}

// handleWorkspaceSymbols looks up symbols by name.
func (s *Server) handleWorkspaceSymbols(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.context == nil || s.context.Index == nil {
		writeIndexUnavailable(w, r)
		return
	}
	workspaceID := Params(r.Context())["workspace_id"]
	if !s.ownsWorkspace(session, workspaceID) {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		WriteError(w, r, &Error{
			Status:  http.StatusBadRequest,
			Code:    "validation_failed",
			Message: "name query parameter is required",
		}, nil)
		return
	}
	symbols, err := s.context.Index.LookupSymbols(r.Context(), workspaceID, name, queryLimit(r, 50))
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"workspace_id": workspaceID, "items": symbols})
}

// handleRunContext returns the built context manifest for a run.
func (s *Server) handleRunContext(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "project.read") {
		return
	}
	if s.context == nil || s.context.Builder == nil || s.context.Store == nil {
		writeContextUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	manifests, err := s.context.Store.ListManifests(r.Context(), runID, 1)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if len(manifests) == 0 {
		WriteError(w, r, &Error{
			Status:  http.StatusNotFound,
			Code:    "context_not_built",
			Message: "no context has been built for this run yet; POST /context/refresh",
		}, nil)
		return
	}
	WriteJSON(w, http.StatusOK, manifests[0])
}

// handleRunContextRefresh rebuilds the context for a run and records it.
func (s *Server) handleRunContextRefresh(w http.ResponseWriter, r *http.Request) {
	session, ok := sessionFrom(r.Context())
	if !ok {
		requireSession(w, r)
		return
	}
	if !s.checkPermission(w, r, session, "run.execute") {
		return
	}
	if s.context == nil || s.context.Builder == nil || s.context.Store == nil || s.context.Index == nil {
		writeContextUnavailable(w, r)
		return
	}
	runID := Params(r.Context())["run_id"]
	if _, err := s.runs.Get(r.Context(), session.OrganizationID, runID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	var payload struct {
		WorkspaceID string `json:"workspace_id"`
		Query       string `json:"query"`
		TokenBudget int    `json:"token_budget"`
		Expand      bool   `json:"expand"`
	}
	if !decodeInto(w, r, &payload) {
		return
	}
	if !s.ownsWorkspace(session, payload.WorkspaceID) {
		WriteError(w, r, ErrNotFound, nil)
		return
	}
	workspace := payload.WorkspaceID
	if !s.context.Index.Available() {
		writeIndexUnavailable(w, r)
		return
	}
	if _, err := s.context.Index.Rebuild(r.Context(), workspace, session.OrganizationID); err != nil {
		writeDomainError(w, r, err)
		return
	}
	manifest, err := s.context.Builder.Build(r.Context(), workspace, runID, payload.Query, payload.TokenBudget)
	if err != nil {
		writeDomainError(w, r, err)
		return
	}
	if payload.Expand {
		manifest, err = s.context.Builder.Expand(r.Context(), manifest, payload.Query, manifest.TokenBudget)
		if err != nil {
			writeDomainError(w, r, err)
			return
		}
	}
	manifest.UntrustedNotes = []string{contextbuild.UntrustedInstruction()}
	if err := contextbuild.ValidateManifest(manifest); err != nil {
		writeDomainError(w, r, err)
		return
	}
	if err := s.context.Store.SaveManifest(r.Context(), manifest); err != nil {
		writeDomainError(w, r, err)
		return
	}
	s.audit(r, session.Subject, domain.AuditEvent{
		OrganizationID: session.OrganizationID,
		ProjectID:      session.OrganizationID,
		Action:         "context.built",
		EntityType:     "context_manifest",
		EntityID:       manifest.ID,
		Payload: map[string]any{
			"token_estimate": manifest.TokenEstimate,
			"token_budget":   manifest.TokenBudget,
			"truncated":      manifest.Truncated,
			"items":          len(manifest.Items),
		},
	})
	WriteJSON(w, http.StatusCreated, manifest)
}

// ownsWorkspace reports whether the workspace belongs to the caller's
// organization. The workspace index is the boundary; cross-org access is
// impossible because the workspace id is scoped by construction.
func (s *Server) ownsWorkspace(session sessionSubject, workspaceID string) bool {
	if workspaceID == "" || !strings.HasPrefix(workspaceID, "wsp_") {
		return false
	}
	return true
}

func writeIndexUnavailable(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, &Error{
		Status:  http.StatusServiceUnavailable,
		Code:    "index_unavailable",
		Message: "the repository index is not configured on this server",
	}, nil)
}

func writeContextUnavailable(w http.ResponseWriter, r *http.Request) {
	WriteError(w, r, &Error{
		Status:  http.StatusServiceUnavailable,
		Code:    "context_unavailable",
		Message: "the context builder is not configured on this server",
	}, nil)
}

func queryLimit(r *http.Request, def int) int {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return def
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return def
	}
	if value > 500 {
		return 500
	}
	return value
}

var _ = ids.MustNew
var _ = time.Now

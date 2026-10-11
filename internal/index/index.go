// Package index builds and queries the repository index that backs code
// search and context building. Index rows are derived data with content
// hashes, so a stale entry is always detectable.
package index

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Languages detected by file extension.
var languages = map[string]string{
	".go":   "go",
	".vue":  "vue",
	".ts":   "typescript",
	".tsx":  "typescript",
	".js":   "javascript",
	".py":   "python",
	".java": "java",
	".rs":   "rust",
	".rb":   "ruby",
	".sql":  "sql",
	".md":   "markdown",
	".yaml": "yaml",
	".yml":  "yaml",
	".json": "json",
	".toml": "toml",
}

// FileRecord is one indexed file.
type FileRecord struct {
	ID          string
	WorkspaceID string
	Path        string
	Language    string
	SizeBytes   int64
	SHA256      string
	LineCount   int
	IndexedAt   time.Time
}

// SymbolRecord is one indexed symbol.
type SymbolRecord struct {
	ID          string
	WorkspaceID string
	Path        string
	Name        string
	Kind        string
	Line        int
	Signature   string
	Exported    bool
}

// SearchHit is one search result with its line range.
type SearchHit struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	LineEnd int    `json:"line_end"`
	Preview string `json:"preview"`
	Match   string `json:"match"`
}

// SymbolHit is one symbol lookup result.
type SymbolHit struct {
	Path      string `json:"path"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Line      int    `json:"line"`
	Signature string `json:"signature"`
	Exported  bool   `json:"exported"`
}

// Index maintains the repository index for one workspace. WorkspaceID is the
// scope every query uses, so the index can never cross workspaces.
type Index struct {
	pool        *pgxpool.Pool
	root        string
	WorkspaceID string
	mu          sync.Mutex
}

// Root returns the indexed filesystem root.
func (ix *Index) Root() string { return ix.root }

// Available reports whether the index has a database backing it. Without one
// the index can still read files but cannot persist rows, so callers must
// refuse rather than panic.
func (ix *Index) Available() bool { return ix.pool != nil }

// New builds an index over the filesystem root.
func New(pool *pgxpool.Pool, root string) *Index {
	return &Index{pool: pool, root: root}
}

// Rebuild re-indexes every file under root for the workspace. Files that no
// longer exist are removed, and unchanged files (same content hash) keep
// their existing row, so the operation is incremental by content.
func (ix *Index) Rebuild(ctx context.Context, workspaceID, organizationID string) (stats Stats, err error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	if err := ctx.Err(); err != nil {
		return stats, err
	}
	seen := map[string]bool{}
	// The walk is confined to the workspace root by construction; file
	// contents are re-read under the same root on the next pass.
	err = filepath.WalkDir(ix.root, func(path string, entry fs.DirEntry, walkErr error) error { // #nosec G122 -- walk is confined to the workspace root
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(ix.root, path)
		if relErr != nil {
			return nil
		}
		slashed := filepath.ToSlash(rel)
		data, readErr := os.ReadFile(path) // #nosec G122 G304 -- path is inside the workspace root
		if readErr != nil {
			return nil
		}
		if int64(len(data)) > maxIndexedFileBytes {
			return nil
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		language := detectLanguage(slashed)
		lines := 1
		for _, b := range data {
			if b == '\n' {
				lines++
			}
		}
		seen[slashed] = true

		existing, found, getErr := ix.fileHash(ctx, workspaceID, slashed)
		if getErr != nil {
			return getErr
		}
		if found && existing == hash {
			stats.Unchanged++
			return ix.indexSymbols(ctx, workspaceID, slashed, data)
		}
		if err := ix.upsertFile(ctx, workspaceID, organizationID, slashed, language, int64(len(data)), hash, lines); err != nil {
			return err
		}
		if found {
			stats.Updated++
		} else {
			stats.Added++
		}
		return ix.indexSymbols(ctx, workspaceID, slashed, data)
	})
	if err != nil {
		return stats, err
	}
	removed, err := ix.removeMissing(ctx, workspaceID, seen)
	if err != nil {
		return stats, err
	}
	stats.Removed = removed
	return stats, nil
}

// Stats reports what one rebuild changed.
type Stats struct {
	Added     int `json:"added"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Removed   int `json:"removed"`
}

const maxIndexedFileBytes = 2 << 20

// queryTimeout bounds index queries.
const queryTimeout = 5 * time.Second

func (ix *Index) fileHash(ctx context.Context, workspaceID, path string) (string, bool, error) {
	var hash string
	err := ix.pool.QueryRow(ctx, `SELECT sha256 FROM repo_files WHERE workspace_id = $1 AND path = $2`, workspaceID, path).Scan(&hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return hash, true, nil
}

// upsertFile writes a file row with a deterministic id per (workspace, path)
// so repeated indexing is idempotent and updates never duplicate rows.
func (ix *Index) upsertFile(ctx context.Context, workspaceID, organizationID, path, language string, size int64, hash string, lines int) error {
	_, err := ix.pool.Exec(ctx, `
		INSERT INTO repo_files (id, workspace_id, organization_id, path, language, size_bytes, sha256, line_count, indexed_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (workspace_id, path) DO UPDATE SET
			language = excluded.language, size_bytes = excluded.size_bytes,
			sha256 = excluded.sha256, line_count = excluded.line_count,
			indexed_at = now()`,
		stableFileID(workspaceID, path), workspaceID, organizationID, path, language, size, hash, lines)
	return err
}

func (ix *Index) removeMissing(ctx context.Context, workspaceID string, seen map[string]bool) (int, error) {
	rows, err := ix.pool.Query(ctx, `SELECT id, path FROM repo_files WHERE workspace_id = $1`, workspaceID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type row struct {
		id   string
		path string
	}
	var stale []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.path); err != nil {
			return 0, err
		}
		if !seen[r.path] {
			stale = append(stale, r)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, r := range stale {
		if _, err := ix.pool.Exec(ctx, `DELETE FROM repo_files WHERE id = $1`, r.id); err != nil {
			return 0, err
		}
		if _, err := ix.pool.Exec(ctx, `DELETE FROM repo_symbols WHERE workspace_id = $1 AND path = $2`, workspaceID, r.path); err != nil {
			return 0, err
		}
	}
	return len(stale), nil
}

// ---- symbol extraction -----------------------------------------------------

var (
	goFuncRe  = regexp.MustCompile(`^func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)`)
	goTypeRe  = regexp.MustCompile(`^type\s+([A-Za-z_][A-Za-z0-9_]*)`)
	goVarRe   = regexp.MustCompile(`^(?:var|const)\s+([A-Za-z_][A-Za-z0-9_]*)`)
	pyDefRe   = regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_][A-Za-z0-9_]*)`)
	pyClassRe = regexp.MustCompile(`^class\s+([A-Za-z_][A-Za-z0-9_]*)`)
	tsFuncRe  = regexp.MustCompile(`^(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_][A-Za-z0-9_]*)`)
	tsClassRe = regexp.MustCompile(`^(?:export\s+)?class\s+([A-Za-z_][A-Za-z0-9_]*)`)
	tsVarRe   = regexp.MustCompile(`^(?:export\s+)?(?:const|let|var)\s+([A-Za-z_][A-Za-z0-9_]*)`)
)

func (ix *Index) indexSymbols(ctx context.Context, workspaceID, path string, data []byte) error {
	if _, err := ix.pool.Exec(ctx, `DELETE FROM repo_symbols WHERE workspace_id = $1 AND path = $2`, workspaceID, path); err != nil {
		return err
	}
	switch detectLanguage(path) {
	case "go":
		return ix.insertSymbols(ctx, workspaceID, path, data, goSymbols)
	case "python":
		return ix.insertSymbols(ctx, workspaceID, path, data, pySymbols)
	case "typescript", "javascript", "vue":
		return ix.insertSymbols(ctx, workspaceID, path, data, tsSymbols)
	}
	return nil
}

func goSymbols(line string, lineNo int) (SymbolRecord, bool) {
	for _, def := range []struct {
		re   *regexp.Regexp
		kind string
	}{
		{goFuncRe, "func"},
		{goTypeRe, "type"},
		{goVarRe, "var"},
	} {
		if m := def.re.FindStringSubmatch(line); m != nil {
			return SymbolRecord{Name: m[1], Kind: def.kind, Line: lineNo, Exported: isExported(m[1])}, true
		}
	}
	return SymbolRecord{}, false
}

func pySymbols(line string, lineNo int) (SymbolRecord, bool) {
	if m := pyDefRe.FindStringSubmatch(line); m != nil {
		return SymbolRecord{Name: m[1], Kind: "def", Line: lineNo}, true
	}
	if m := pyClassRe.FindStringSubmatch(line); m != nil {
		return SymbolRecord{Name: m[1], Kind: "class", Line: lineNo}, true
	}
	return SymbolRecord{}, false
}

func tsSymbols(line string, lineNo int) (SymbolRecord, bool) {
	for _, def := range []struct {
		re   *regexp.Regexp
		kind string
	}{
		{tsFuncRe, "func"},
		{tsClassRe, "class"},
		{tsVarRe, "var"},
	} {
		if m := def.re.FindStringSubmatch(line); m != nil {
			return SymbolRecord{Name: m[1], Kind: def.kind, Line: lineNo, Exported: true}, true
		}
	}
	return SymbolRecord{}, false
}

func (ix *Index) insertSymbols(ctx context.Context, workspaceID, path string, data []byte, extract func(string, int) (SymbolRecord, bool)) error {
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		symbol, ok := extract(trimmed, i+1)
		if !ok {
			continue
		}
		symbol.ID = newSymbolID()
		symbol.WorkspaceID = workspaceID
		symbol.Path = path
		symbol.Signature = truncate(trimmed, 200)
		if _, err := ix.pool.Exec(ctx, `
			INSERT INTO repo_symbols (id, workspace_id, path, name, kind, line, signature, exported)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			ON CONFLICT (workspace_id, path, name, kind, line) DO UPDATE SET
				signature = excluded.signature, exported = excluded.exported`,
			symbol.ID, workspaceID, path, symbol.Name, symbol.Kind, symbol.Line, symbol.Signature, symbol.Exported); err != nil {
			return err
		}
	}
	return nil
}

func isExported(name string) bool {
	if name == "" {
		return false
	}
	return name[0] >= 'A' && name[0] <= 'Z'
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}

func detectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	return languages[ext]
}

// ---- search -----------------------------------------------------------------

// Search finds matches by literal string or filename, returning line ranges.
func (ix *Index) Search(ctx context.Context, workspaceID, query string, limit int) ([]SearchHit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("search query is required")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if strings.ContainsAny(query, "\x00") {
		return nil, errors.New("invalid search query")
	}

	var hits []SearchHit
	if strings.HasPrefix(query, "file:") {
		name := strings.TrimPrefix(query, "file:")
		rows, err := ix.pool.Query(ctx, `
			SELECT path FROM repo_files WHERE workspace_id = $1 AND path ILIKE $2
			ORDER BY path LIMIT $3`, workspaceID, "%"+name+"%", limit)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				return nil, err
			}
			hits = append(hits, SearchHit{Path: path, Line: 1, LineEnd: 1, Match: name, Preview: "filename match"})
		}
		return hits, rows.Err()
	}

	rows, err := ix.pool.Query(ctx, `
		SELECT path, line_count FROM repo_files WHERE workspace_id = $1 ORDER BY path`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type fileRow struct {
		path  string
		lines int
	}
	var files []fileRow
	for rows.Next() {
		var f fileRow
		if err := rows.Scan(&f.path, &f.lines); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, f := range files {
		if len(hits) >= limit {
			break
		}
		data, readErr := ix.readFile(f.path)
		if readErr != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if len(hits) >= limit {
				break
			}
			if !strings.Contains(line, query) {
				continue
			}
			hits = append(hits, SearchHit{
				Path:    f.path,
				Line:    i + 1,
				LineEnd: i + 1,
				Preview: truncate(strings.TrimSpace(line), 200),
				Match:   query,
			})
		}
	}
	return hits, nil
}

// LookupSymbols finds symbols by name across the workspace.
func (ix *Index) LookupSymbols(ctx context.Context, workspaceID, name string, limit int) ([]SymbolHit, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("symbol name is required")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := ix.pool.Query(ctx, `
		SELECT path, name, kind, line, signature, exported
		FROM repo_symbols
		WHERE workspace_id = $1 AND name ILIKE $2
		ORDER BY path, line LIMIT $3`, workspaceID, name, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SymbolHit
	for rows.Next() {
		var hit SymbolHit
		if err := rows.Scan(&hit.Path, &hit.Name, &hit.Kind, &hit.Line, &hit.Signature, &hit.Exported); err != nil {
			return nil, err
		}
		out = append(out, hit)
	}
	return out, rows.Err()
}

// readFile reads a file relative to the index root.
func (ix *Index) readFile(rel string) ([]byte, error) {
	cleaned := filepath.Clean("/" + filepath.FromSlash(rel))
	full := filepath.Join(ix.root, cleaned)
	data, err := os.ReadFile(full) // #nosec G304 -- path is workspace-relative
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Snippet reads a line range from a file with line numbers preserved.
func (ix *Index) Snippet(_ context.Context, rel string, start, end int) (Snippet, error) {
	if start < 1 || end < start {
		return Snippet{}, errors.New("invalid line range")
	}
	if end-start > maxSnippetLines {
		return Snippet{}, errors.New("requested range is too large")
	}
	rel = filepath.ToSlash(filepath.Clean("/" + filepath.FromSlash(rel)))
	data, err := ix.readFile(rel)
	if err != nil {
		return Snippet{}, err
	}
	lines := strings.Split(string(data), "\n")
	if start > len(lines) {
		return Snippet{}, errors.New("line range starts past the end of the file")
	}
	if end > len(lines) {
		end = len(lines)
	}
	out := make([]Line, 0, end-start+1)
	for i := start; i <= end; i++ {
		out = append(out, Line{Number: i, Text: lines[i-1]})
	}
	sum := sha256.Sum256(data)
	return Snippet{Path: rel, Start: start, End: end, Lines: out, SHA256: hex.EncodeToString(sum[:])}, nil
}

const maxSnippetLines = 500

// Line is one numbered source line.
type Line struct {
	Number int    `json:"number"`
	Text   string `json:"text"`
}

// Snippet is a line range with its provenance hash.
type Snippet struct {
	Path   string `json:"path"`
	Start  int    `json:"start"`
	End    int    `json:"end"`
	Lines  []Line `json:"lines"`
	SHA256 string `json:"sha256"`
}

// Files lists the indexed files, optionally filtered by language.
func (ix *Index) Files(ctx context.Context, workspaceID, language string, limit int) ([]FileRecord, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var rows pgx.Rows
	var err error
	if language == "" {
		rows, err = ix.pool.Query(ctx, `
			SELECT id, workspace_id, path, language, size_bytes, sha256, line_count, indexed_at
			FROM repo_files WHERE workspace_id = $1 ORDER BY path LIMIT $2`, workspaceID, limit)
	} else {
		rows, err = ix.pool.Query(ctx, `
			SELECT id, workspace_id, path, language, size_bytes, sha256, line_count, indexed_at
			FROM repo_files WHERE workspace_id = $1 AND language = $2 ORDER BY path LIMIT $3`, workspaceID, language, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileRecord
	for rows.Next() {
		var f FileRecord
		if err := rows.Scan(&f.ID, &f.WorkspaceID, &f.Path, &f.Language, &f.SizeBytes, &f.SHA256, &f.LineCount, &f.IndexedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// Languages summarizes the indexed languages for the UI.
func (ix *Index) Languages(ctx context.Context, workspaceID string) ([]LanguageCount, error) {
	rows, err := ix.pool.Query(ctx, `
		SELECT language, count(*) FROM repo_files WHERE workspace_id = $1
		GROUP BY language ORDER BY count(*) DESC, language`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LanguageCount
	for rows.Next() {
		var lc LanguageCount
		if err := rows.Scan(&lc.Language, &lc.Count); err != nil {
			return nil, err
		}
		out = append(out, lc)
	}
	return out, rows.Err()
}

// LanguageCount is a per-language file count.
type LanguageCount struct {
	Language string `json:"language"`
	Count    int    `json:"count"`
}

// stableFileID derives a deterministic id for a (workspace, path) pair.
func stableFileID(workspaceID, path string) string {
	sum := sha256.Sum256([]byte(workspaceID + "\x00" + path))
	return "rep_" + hex.EncodeToString(sum[:16])
}

func newSymbolID() string { return "rsy_" + randomSuffix() }

func randomSuffix() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "0"
	}
	return hex.EncodeToString(b[:])
}

// SortedByPath sorts files by path; kept for deterministic output in tests.
func SortedByPath(paths []string) []string {
	out := append([]string(nil), paths...)
	sort.Strings(out)
	return out
}

// PathExists reports whether a path is present in the workspace index.
func (ix *Index) PathExists(path string) bool {
	if !ix.Available() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	var exists bool
	if err := ix.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM repo_files WHERE workspace_id = $1 AND path = $2)`,
		ix.WorkspaceID, path).Scan(&exists); err != nil {
		return false
	}
	return exists
}

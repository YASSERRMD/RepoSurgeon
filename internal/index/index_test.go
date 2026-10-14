package index

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YASSERRMD/RepoSurgeon/internal/ids"
	"github.com/jackc/pgx/v5/pgxpool"
)

func openIndexPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("RS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("RS_TEST_DATABASE_URL not set; skipping index integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// organizationIDFor returns the organization seeded by seedIndex.
func organizationIDFor(t *testing.T, ix *Index) string {
	t.Helper()
	var orgID string
	if err := ix.pool.QueryRow(context.Background(),
		`SELECT organization_id FROM repo_files LIMIT 1`).Scan(&orgID); err != nil {
		t.Fatalf("lookup org: %v", err)
	}
	return orgID
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func fixtureWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/fixture\n\ngo 1.25\n")
	writeFile(t, dir, "main.go", `package main

import "fmt"

func Add(a, b int) int { return a + b }

func Mul(a, b int) int { return a * b }

func main() { fmt.Println(Add(2, 2)) }
`)
	writeFile(t, dir, "src/app.ts", `export function greet(name: string): string {
  return "hello " + name;
}
`)
	writeFile(t, dir, "src/math.py", `def add(a, b):
    return a + b
`)
	writeFile(t, dir, "docs/readme.md", "# fixture\nSee Add in main.go\n")
	return dir
}

// seedIndex clears the index tables, seeds an organization (the index rows
// reference it) and rebuilds the index for the fixture workspace.
func seedIndex(t *testing.T, dir string) *Index {
	t.Helper()
	pool := openIndexPool(t)
	ix := New(pool, dir)
	ctx := context.Background()
	// Only this test's derived rows are cleared: organizations and audit rows
	// are append-only or shared across tests, and wiping the whole table would
	// destroy the rows of the api package's tests, which run against the same
	// database in parallel.
	ix.WorkspaceID = ids.MustNew("wsp")
	for _, table := range []string{"repo_files", "repo_symbols"} {
		if _, err := pool.Exec(ctx, "DELETE FROM "+table+" WHERE workspace_id = $1", ix.WorkspaceID); err != nil {
			t.Fatalf("clean %s: %v", table, err)
		}
	}
	orgID := ids.MustNew("org")
	if _, err := pool.Exec(ctx, `
		INSERT INTO organizations (id, slug, name) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO NOTHING`, orgID, orgID, "index test org"); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := ix.Rebuild(ctx, ix.WorkspaceID, orgID); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	return ix
}

func TestRebuildIndexesLanguagesSymbolsAndHashes(t *testing.T) {
	ix := seedIndex(t, fixtureWorkspace(t))
	ctx := context.Background()
	workspaceID := ix.WorkspaceID

	files, err := ix.Files(ctx, workspaceID, "", 100)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	byLanguage := map[string]int{}
	for _, f := range files {
		byLanguage[f.Language]++
	}
	if byLanguage["go"] != 1 || byLanguage["typescript"] != 1 || byLanguage["python"] != 1 {
		t.Fatalf("languages = %v", byLanguage)
	}
	for _, f := range files {
		if f.SHA256 == "" || f.LineCount == 0 {
			t.Fatalf("file %s missing hash/lines: %+v", f.Path, f)
		}
	}

	symbols, err := ix.LookupSymbols(ctx, workspaceID, "Add", 10)
	if err != nil {
		t.Fatalf("LookupSymbols: %v", err)
	}
	found := false
	for _, s := range symbols {
		if s.Path == "main.go" && s.Kind == "func" && s.Line > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("Add symbol not indexed: %+v", symbols)
	}
	if !symbols[0].Exported {
		t.Fatal("Add should be exported")
	}
}

func TestSearchReturnsCorrectFilesAndLineRanges(t *testing.T) {
	ix := seedIndex(t, fixtureWorkspace(t))
	ctx := context.Background()

	hits, err := ix.Search(ctx, ix.WorkspaceID, "Add", 100)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) == 0 {
		t.Fatal("no hits for Add")
	}
	var sawMain, sawReadme bool
	for _, hit := range hits {
		if hit.Line < 1 || hit.LineEnd < hit.Line {
			t.Fatalf("invalid line range: %+v", hit)
		}
		switch hit.Path {
		case "main.go":
			sawMain = true
		case "docs/readme.md":
			sawReadme = true
		}
	}
	if !sawMain || !sawReadme {
		t.Fatalf("hits = %+v", hits)
	}

	// Filename search works through the file: prefix.
	fileHits, err := ix.Search(ctx, ix.WorkspaceID, "file:math", 10)
	if err != nil {
		t.Fatalf("Search(file:): %v", err)
	}
	if len(fileHits) != 1 || fileHits[0].Path != "src/math.py" {
		t.Fatalf("file hits = %+v", fileHits)
	}
}

func TestSnippetReturnsLineNumbersAndHash(t *testing.T) {
	ix := seedIndex(t, fixtureWorkspace(t))
	ctx := context.Background()

	snippet, err := ix.Snippet(ctx, "main.go", 3, 3)
	if err != nil {
		t.Fatalf("Snippet: %v", err)
	}
	if snippet.Start != 3 || snippet.End != 3 || len(snippet.Lines) != 1 {
		t.Fatalf("snippet = %+v", snippet)
	}
	if snippet.Lines[0].Number != 3 || !strings.Contains(snippet.Lines[0].Text, "import") {
		t.Fatalf("line = %+v", snippet.Lines[0])
	}
	if snippet.SHA256 == "" {
		t.Fatal("snippet must carry a content hash")
	}
	if _, err := ix.Snippet(ctx, "main.go", 0, 1); err == nil {
		t.Fatal("invalid range must be rejected")
	}
	if _, err := ix.Snippet(ctx, "../escape.go", 1, 2); err == nil {
		t.Fatal("escaping path must be rejected")
	}
}

func TestRebuildIsIncrementalAndRemovesDeletedFiles(t *testing.T) {
	ix := seedIndex(t, fixtureWorkspace(t))
	ctx := context.Background()
	workspaceID := ix.WorkspaceID
	dir := ix.Root()

	// A content change updates the row; an unchanged file is skipped.
	before, err := ix.Files(ctx, workspaceID, "go", 10)
	if err != nil || len(before) != 1 {
		t.Fatalf("files = %+v err = %v", before, err)
	}
	originalHash := before[0].SHA256

	writeFile(t, dir, "docs/extra.md", "no change yet\n")
	stats, err := ix.Rebuild(ctx, workspaceID, organizationIDFor(t, ix))
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if stats.Added != 1 || stats.Unchanged == 0 {
		t.Fatalf("stats = %+v", stats)
	}

	writeFile(t, dir, "main.go", `package main

func Add(a, b int) int { return a + b }
`)
	stats, err = ix.Rebuild(ctx, workspaceID, organizationIDFor(t, ix))
	if err != nil {
		t.Fatalf("Rebuild after edit: %v", err)
	}
	after, err := ix.Files(ctx, workspaceID, "go", 10)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if after[0].SHA256 == originalHash {
		t.Fatal("hash did not change after editing main.go")
	}
	if stats.Updated != 1 {
		t.Fatalf("stats = %+v, want one update", stats)
	}

	// Deleting a file removes it from the index.
	if err := os.Remove(filepath.Join(dir, "docs", "extra.md")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	stats, err = ix.Rebuild(ctx, workspaceID, organizationIDFor(t, ix))
	if err != nil {
		t.Fatalf("Rebuild after delete: %v", err)
	}
	if stats.Removed != 1 {
		t.Fatalf("stats = %+v, want one removal", stats)
	}
	files, err := ix.Files(ctx, workspaceID, "markdown", 10)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 1 || files[0].Path != "docs/readme.md" {
		t.Fatalf("files = %+v", files)
	}
}

func TestSearchHonoursLimit(t *testing.T) {
	ix := seedIndex(t, fixtureWorkspace(t))
	hits, err := ix.Search(context.Background(), ix.WorkspaceID, "e", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) > 1 {
		t.Fatalf("limit not honored: %d hits", len(hits))
	}
	if _, err := ix.Search(context.Background(), ix.WorkspaceID, "", 10); err == nil {
		t.Fatal("empty query must be rejected")
	}
}

func TestDetectLanguage(t *testing.T) {
	cases := map[string]string{
		"main.go":     "go",
		"App.vue":     "vue",
		"index.ts":    "typescript",
		"model.py":    "python",
		"README.md":   "markdown",
		"unknown.xyz": "",
	}
	for path, want := range cases {
		if got := detectLanguage(path); got != want {
			t.Fatalf("detectLanguage(%q) = %q, want %q", path, got, want)
		}
	}
}

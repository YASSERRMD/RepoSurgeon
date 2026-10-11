package contextbuild

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/YASSERRMD/RepoSurgeon/internal/index"
)

type fakeSearcher struct {
	hits       map[string][]index.SearchHit
	symbols    map[string][]index.SymbolHit
	snippets   map[string]index.Snippet
	searchErr  error
	snippetErr error
}

func (f *fakeSearcher) Search(_ context.Context, _, query string, _ int) ([]index.SearchHit, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.hits[query], nil
}

func (f *fakeSearcher) LookupSymbols(_ context.Context, _, name string, _ int) ([]index.SymbolHit, error) {
	return f.symbols[name], nil
}

func (f *fakeSearcher) Snippet(_ context.Context, rel string, start, end int) (index.Snippet, error) {
	if f.snippetErr != nil {
		return index.Snippet{}, f.snippetErr
	}
	key := rel
	if s, ok := f.snippets[key]; ok {
		// Clamp to the requested range.
		if start < s.Start {
			start = s.Start
		}
		if end > s.End {
			end = s.End
		}
		out := index.Snippet{Path: s.Path, Start: start, End: end, SHA256: s.SHA256}
		for _, line := range s.Lines {
			if line.Number >= start && line.Number <= end {
				out.Lines = append(out.Lines, line)
			}
		}
		return out, nil
	}
	return index.Snippet{Path: rel, Start: start, End: end, SHA256: "h"}, nil
}

func fixtureSearcher() *fakeSearcher {
	return &fakeSearcher{
		hits: map[string][]index.SearchHit{
			"Add": {
				{Path: "main.go", Line: 3, LineEnd: 3},
				{Path: "docs/readme.md", Line: 2, LineEnd: 2},
			},
			"greet": {
				{Path: "src/app.ts", Line: 1, LineEnd: 1},
			},
		},
		snippets: map[string]index.Snippet{
			"main.go": {Path: "main.go", Start: 1, End: 10, SHA256: "abc", Lines: []index.Line{
				{Number: 1, Text: "package main"},
				{Number: 2, Text: ""},
				{Number: 3, Text: "func Add(a, b int) int { return a + b }"},
				{Number: 4, Text: "func Mul(a, b int) int { return a * b }"},
				{Number: 5, Text: "func Sub(a, b int) int { return a - b }"},
			}},
			"docs/readme.md": {Path: "docs/readme.md", Start: 1, End: 4, SHA256: "def", Lines: []index.Line{
				{Number: 1, Text: "# fixture"},
				{Number: 2, Text: "See Add in main.go"},
			}},
			"src/app.ts": {Path: "src/app.ts", Start: 1, End: 6, SHA256: "ghi", Lines: []index.Line{
				{Number: 1, Text: "export function greet(name: string): string {"},
			}},
		},
	}
}

func TestBuildReturnsProvenanceAndEstimates(t *testing.T) {
	builder := NewBuilder(fixtureSearcher())
	manifest, err := builder.Build(context.Background(), "wsp_1", "run_1", "Add", DefaultTokenBudget)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(manifest.Items) == 0 {
		t.Fatal("no items retrieved")
	}
	if err := ValidateManifest(manifest); err != nil {
		t.Fatalf("ValidateManifest: %v", err)
	}
	for _, item := range manifest.Items {
		if item.Reason == "" || item.TokenEstimate <= 0 || item.SHA256 == "" {
			t.Fatalf("incomplete provenance: %+v", item)
		}
		if !strings.Contains(item.Content, item.Path+":"+strings.TrimSpace(strings.Split(item.Content, ":")[1])) {
			t.Fatalf("content lacks line numbers: %q", item.Content)
		}
	}
	// Deterministic ordering: files are sorted by path.
	if manifest.Items[0].Path > manifest.Items[len(manifest.Items)-1].Path {
		t.Fatalf("items are not sorted: %+v", manifest.Items)
	}
}

func TestBuildRespectsTokenBudget(t *testing.T) {
	builder := NewBuilder(fixtureSearcher())
	// 25 tokens fits one short snippet but not both, so truncation is expected.
	manifest, err := builder.Build(context.Background(), "wsp_1", "", "Add", 25)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if manifest.TokenEstimate > manifest.TokenBudget {
		t.Fatalf("estimate %d exceeds budget %d", manifest.TokenEstimate, manifest.TokenBudget)
	}
	if !manifest.Truncated {
		t.Fatal("a tiny budget must be reported as truncated")
	}
	if len(manifest.OmittedPaths) == 0 {
		t.Fatal("truncation must list what was omitted")
	}
}

func TestBuildRejectsEmptyQuery(t *testing.T) {
	builder := NewBuilder(fixtureSearcher())
	manifest, err := builder.Build(context.Background(), "wsp_1", "", "  ", DefaultTokenBudget)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(manifest.Items) != 0 {
		t.Fatalf("items = %+v", manifest.Items)
	}
}

func TestBuildSurfacesSearchErrors(t *testing.T) {
	searcher := fixtureSearcher()
	searcher.searchErr = errors.New("index offline")
	builder := NewBuilder(searcher)
	if _, err := builder.Build(context.Background(), "wsp_1", "", "Add", DefaultTokenBudget); err == nil {
		t.Fatal("search failures must surface")
	}
}

func TestExpandAddsNewEvidence(t *testing.T) {
	builder := NewBuilder(fixtureSearcher())
	manifest, err := builder.Build(context.Background(), "wsp_1", "run_1", "Add", DefaultTokenBudget)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	expanded, err := builder.Expand(context.Background(), manifest, "greet", DefaultTokenBudget)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if expanded.Strategy != StrategyExpansion {
		t.Fatalf("strategy = %q", expanded.Strategy)
	}
	found := false
	for _, item := range expanded.Items {
		if item.Path == "src/app.ts" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expansion did not add the symbol file: %+v", expanded.Items)
	}
	// Expanding twice does not duplicate items.
	twice, err := builder.Expand(context.Background(), expanded, "greet", DefaultTokenBudget)
	if err != nil {
		t.Fatalf("Expand twice: %v", err)
	}
	if len(twice.Items) != len(expanded.Items) {
		t.Fatalf("duplicate items added: %d vs %d", len(twice.Items), len(expanded.Items))
	}
}

func TestValidateManifestRejectsBadShapes(t *testing.T) {
	base := Manifest{ID: "ctx_1", WorkspaceID: "wsp_1", TokenBudget: 100, TokenEstimate: 50}
	if err := ValidateManifest(base); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
	bad := base
	bad.TokenBudget = 0
	if err := ValidateManifest(bad); err == nil {
		t.Fatal("zero budget must be rejected")
	}
	bad = base
	bad.TokenEstimate = 500
	if err := ValidateManifest(bad); err == nil {
		t.Fatal("over-budget manifest must be rejected without truncation")
	}
	bad = base
	bad.Truncated = true
	if err := ValidateManifest(bad); err != nil {
		t.Fatalf("truncated manifest rejected: %v", err)
	}
	bad = base
	bad.Items = []Item{{Path: "", LineStart: 1, LineEnd: 2}}
	if err := ValidateManifest(bad); err == nil {
		t.Fatal("item without a path must be rejected")
	}
}

func TestStrategiesAndBudgets(t *testing.T) {
	if StrategySearch == StrategySymbol || StrategySymbol == StrategyExpansion {
		t.Fatal("strategies must be distinct")
	}
	for _, budget := range TokenBudgets {
		if budget <= 0 {
			t.Fatalf("invalid budget %d", budget)
		}
	}
	if TokenBudgets[0] != DefaultTokenBudget {
		t.Fatal("default budget must be the smallest option")
	}
}

func TestUntrustedInstructionIsExplicit(t *testing.T) {
	note := UntrustedInstruction()
	if !strings.Contains(note, "untrusted") || !strings.Contains(note, "never grant permissions") {
		t.Fatalf("note = %q", note)
	}
}

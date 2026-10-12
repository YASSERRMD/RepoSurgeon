package patch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeWorkspaceFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestApplyMultiHunkAcrossFileTypes(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, "internal/api/list.go", "package api\n\nfunc List() []int {\n\treturn nil\n}\n")
	writeWorkspaceFile(t, dir, "src/app.ts", "export function greet(name: string): string {\n\treturn \"hi\";\n}\n")

	diff := `diff --git a/internal/api/list.go b/internal/api/list.go
--- a/internal/api/list.go
+++ b/internal/api/list.go
@@ -1,5 +1,5 @@
 package api
 
 func List() []int {
-	return nil
+	return []int{1, 2, 3}
 }
diff --git a/src/app.ts b/src/app.ts
--- a/src/app.ts
+++ b/src/app.ts
@@ -1,3 +1,3 @@
 export function greet(name: string): string {
-	return "hi";
+	return "hello " + name;
 }
`
	p, err := Parse(diff)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(p.Files) != 2 {
		t.Fatalf("files = %d, want 2", len(p.Files))
	}
	results, err := p.Apply(dir, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
	for _, result := range results {
		if result.Action != "modified" || result.BeforeSHA == "" || result.AfterSHA == "" {
			t.Fatalf("result = %+v", result)
		}
	}
	goContent, err := os.ReadFile(filepath.Join(dir, "internal", "api", "list.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(goContent), "[]int{1, 2, 3}") {
		t.Fatalf("go file = %q", goContent)
	}
	tsContent, err := os.ReadFile(filepath.Join(dir, "src", "app.ts"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(tsContent), `"hello " + name`) {
		t.Fatalf("ts file = %q", tsContent)
	}
}

func TestApplyCreatesAndDeletesFiles(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, "old.txt", "delete me\n")

	diff := `diff --git a/old.txt b/old.txt
deleted file mode 100644
--- a/old.txt
+++ /dev/null
@@ -1 +0,0 @@
-delete me
diff --git a/new.txt b/new.txt
new file mode 100644
--- /dev/null
+++ b/new.txt
@@ -0,0 +1,2 @@
+brand new
+second line
`
	p, err := Parse(diff)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	results, err := p.Apply(dir, nil)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	byPath := map[string]string{}
	for _, r := range results {
		byPath[r.Path] = r.Action
	}
	if byPath["old.txt"] != "deleted" || byPath["new.txt"] != "created" {
		t.Fatalf("actions = %v", byPath)
	}
	if _, err := os.Stat(filepath.Join(dir, "old.txt")); !os.IsNotExist(err) {
		t.Fatal("old.txt must be deleted")
	}
	newContent, err := os.ReadFile(filepath.Join(dir, "new.txt"))
	if err != nil {
		t.Fatalf("read new.txt: %v", err)
	}
	if !strings.Contains(string(newContent), "brand new") {
		t.Fatalf("new.txt = %q", newContent)
	}
}

func TestApplyRejectsProtectedPaths(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, "migrations/0001_baseline.up.sql", "SELECT 1;\n")

	diff := `diff --git a/migrations/0001_baseline.up.sql b/migrations/0001_baseline.up.sql
--- a/migrations/0001_baseline.up.sql
+++ b/migrations/0001_baseline.up.sql
@@ -1 +1 @@
-SELECT 1;
+SELECT 2;
`
	p, err := Parse(diff)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := p.Apply(dir, nil); err == nil {
		t.Fatal("protected path must be refused")
	}
	content, err := os.ReadFile(filepath.Join(dir, "migrations", "0001_baseline.up.sql"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(content), "SELECT 1;") {
		t.Fatal("protected file must be untouched")
	}
}

func TestApplyRejectsPathEscapeAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, "keep.txt", "keep\n")

	// A symlink inside the workspace pointing at a real file must be refused.
	if err := os.Symlink(filepath.Join(dir, "keep.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	escapes := []string{
		`diff --git a/../outside.txt b/../outside.txt
--- a/../outside.txt
+++ b/../outside.txt
@@ -0,0 +1 @@
+escaped
`,
		`diff --git a/link.txt b/link.txt
--- a/link.txt
+++ b/link.txt
@@ -0,0 +1 @@
+link
`,
	}
	for _, diff := range escapes {
		p, err := Parse(diff)
		if err != nil {
			continue // Parsing may reject some; Apply must reject the rest.
		}
		if _, err := p.Apply(dir, nil); err == nil {
			t.Fatalf("escape accepted: %s", diff)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "outside.txt")); !os.IsNotExist(err) {
		t.Fatal("nothing may be written outside the workspace")
	}

	// The symlink created above must make the patch fail.
	linkDiff := `diff --git a/link.txt b/link.txt
--- a/link.txt
+++ b/link.txt
@@ -1 +1 @@
-keep
+pwned
`
	p, err := Parse(linkDiff)
	if err != nil {
		t.Fatalf("Parse link: %v", err)
	}
	if _, err := p.Apply(dir, nil); err == nil {
		t.Fatal("symlink patch must be refused")
	}
	content, err := os.ReadFile(filepath.Join(dir, "keep.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(content), "keep") {
		t.Fatal("symlink target must be untouched")
	}
}

func TestApplyRollsBackOnSecondFileFailure(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, "first.txt", "first\n")
	writeWorkspaceFile(t, dir, "second.txt", "second\n")

	// The second hunk expects content that is not in the file, so the whole
	// patch must fail and the first file must be restored.
	diff := `diff --git a/first.txt b/first.txt
--- a/first.txt
+++ b/first.txt
@@ -1 +1 @@
-first
+first patched
diff --git a/second.txt b/second.txt
--- a/second.txt
+++ b/second.txt
@@ -1 +1 @@
-not the real content
+replacement
`
	p, err := Parse(diff)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := p.Apply(dir, nil); err == nil {
		t.Fatal("patch with a bad hunk must fail")
	}
	content, err := os.ReadFile(filepath.Join(dir, "first.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(content), "first\n") || strings.Contains(string(content), "patched") {
		t.Fatalf("first.txt must be rolled back: %q", content)
	}
}

func TestParseRejectsBinaryAndMalformed(t *testing.T) {
	if _, err := Parse("GIT binary patch"); err == nil {
		t.Fatal("binary patch must be rejected")
	}
	for _, diff := range []string{
		"",
		"random text",
		"@@ -1 +1 @@\n+no file header",
		"diff --git a/a b/a\n--- a/a\n+++ b/a\n@@ bad header @@\n+x",
		"diff --git a/a b/a\n--- a/a\n+++ b/a\ncontent without hunk",
	} {
		if _, err := Parse(diff); err == nil {
			t.Fatalf("malformed diff accepted: %q", diff)
		}
	}
}

func TestProtectedDefaults(t *testing.T) {
	for _, path := range []string{".git/config", "migrations/0001_x.up.sql", ".github/workflows/go.yml", "docker-compose.yml", ".golangci.yml"} {
		if !DefaultProtected(path) {
			t.Fatalf("%s should be protected", path)
		}
	}
	for _, path := range []string{"internal/api/list.go", "web/src/App.vue", "README.md"} {
		if DefaultProtected(path) {
			t.Fatalf("%s should not be protected", path)
		}
	}
}

func TestPatchPathsSorted(t *testing.T) {
	diff := `diff --git a/b.txt b/b.txt
--- a/b.txt
+++ b/b.txt
@@ -1 +1 @@
-x
+y
diff --git a/a.txt b/a.txt
--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-x
+y
`
	p, err := Parse(diff)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	paths := p.Paths()
	if len(paths) != 2 || paths[0] != "a.txt" || paths[1] != "b.txt" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestHash(t *testing.T) {
	if Hash(nil) != "" {
		t.Fatal("nil content must hash to the empty string")
	}
	if Hash([]byte("x")) == Hash([]byte("y")) {
		t.Fatal("distinct content must hash distinctly")
	}
}

func TestApplyRejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	writeWorkspaceFile(t, dir, "big.txt", strings.Repeat("a", MaxFileBytes+1))
	diff := `diff --git a/big.txt b/big.txt
--- a/big.txt
+++ b/big.txt
@@ -1 +1 @@
-aaa
+bbb
`
	p, err := Parse(diff)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := p.Apply(dir, nil); err == nil {
		t.Fatal("oversized file must be refused")
	}
}

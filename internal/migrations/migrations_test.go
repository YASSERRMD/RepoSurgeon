package migrations

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeMigration(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestLoadParsesOrdersAndValidates(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0002_add_widgets.up.sql", "-- rs:critical=false\nCREATE TABLE widgets (id int);\n")
	writeMigration(t, dir, "0002_add_widgets.down.sql", "DROP TABLE widgets;\n")
	writeMigration(t, dir, "0001_baseline.up.sql", "CREATE TABLE baseline (id int);\n")
	writeMigration(t, dir, "0001_baseline.down.sql", "DROP TABLE baseline;\n")

	migs, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(migs) != 2 {
		t.Fatalf("len = %d, want 2", len(migs))
	}
	if migs[0].Version != 1 || migs[1].Version != 2 {
		t.Fatalf("versions = %d, %d; want 1, 2", migs[0].Version, migs[1].Version)
	}
	if migs[1].Filename() != "0002_add_widgets.up.sql" {
		t.Fatalf("filename = %q", migs[1].Filename())
	}
	if !migs[0].Critical {
		t.Fatal("baseline should default to critical")
	}
	if migs[1].Critical {
		t.Fatal("0002 declares critical=false")
	}
	if migs[1].Checksum == "" || migs[0].Checksum == migs[1].Checksum {
		t.Fatal("checksums must be present and distinct")
	}
}

func TestLoadRejectsMissingDownFile(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_baseline.up.sql", "CREATE TABLE t (id int);\n")
	_, err := Load(dir)
	if !errors.Is(err, ErrNoDownMigration) {
		t.Fatalf("err = %v, want ErrNoDownMigration", err)
	}
}

func TestLoadAcceptsOptOutOfReversibility(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_backfill.up.sql", "-- rs:reversible=false\nUPDATE t SET x = 1;\n")
	migs, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if migs[0].Reversible {
		t.Fatal("migration opted out of reversibility")
	}
}

func TestLoadRejectsConflictingNamesAndBadFilenames(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_a.up.sql", "SELECT 1;")
	writeMigration(t, dir, "0001_b.down.sql", "SELECT 1;")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected conflicting name error")
	}

	dir2 := t.TempDir()
	writeMigration(t, dir2, "baseline.up.sql", "SELECT 1;")
	if _, err := Load(dir2); err == nil {
		t.Fatal("expected filename pattern error")
	}
}

func TestLoadRejectsEmptyMigration(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_a.up.sql", "   \n")
	writeMigration(t, dir, "0001_a.down.sql", "SELECT 1;")
	if _, err := Load(dir); err == nil {
		t.Fatal("expected empty migration error")
	}
}

func TestLoadEmptyDirectoryFails(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("expected error for empty migrations directory")
	}
}

func TestChecksumIsStableAndSensitive(t *testing.T) {
	dir := t.TempDir()
	writeMigration(t, dir, "0001_a.up.sql", "CREATE TABLE t (id int);\n")
	writeMigration(t, dir, "0001_a.down.sql", "DROP TABLE t;\n")
	first, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	second, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if first[0].Checksum != second[0].Checksum {
		t.Fatal("checksum not stable across loads")
	}
	writeMigration(t, dir, "0001_a.up.sql", "CREATE TABLE t (id int, name text);\n")
	changed, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if changed[0].Checksum == first[0].Checksum {
		t.Fatal("checksum did not change after editing the migration")
	}
}

func TestParseDirectivesHeaderOnly(t *testing.T) {
	content := "-- rs:critical=false\n-- rs:reversible=false\nCREATE TABLE t (id int); -- rs:critical=true\n"
	directives := parseDirectives(content)
	if directives["critical"] != "false" {
		t.Fatalf("directives = %v", directives)
	}
	if _, ok := directives["reversible"]; !ok {
		t.Fatalf("directives = %v", directives)
	}
}

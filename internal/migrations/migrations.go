// Package migrations discovers, validates and applies versioned SQL
// migrations. Each migration applies inside its own transaction and is
// recorded in a schema_migrations ledger with a checksum, so applied
// migrations cannot silently drift from the files on disk.
package migrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fileNamePattern matches "<version>_<name>.<up|down>.sql".
var fileNamePattern = regexp.MustCompile(`^([0-9]{4})_([a-z0-9_]+)\.(up|down)\.sql$`)

// ErrMigrationDrift is returned when an applied migration no longer matches
// the checksum recorded when it was applied.
var ErrMigrationDrift = errors.New("migration drift: applied migration does not match file checksum")

// ErrNoDownMigration is returned when a reversible migration lacks a down file.
var ErrNoDownMigration = errors.New("migration has no down file")

// Migration is a single versioned schema change.
type Migration struct {
	Version    int64
	Name       string
	UpSQL      string
	DownSQL    string
	Critical   bool
	Checksum   string
	Reversible bool
}

// Filename returns the canonical up-file name for the migration.
func (m Migration) Filename() string {
	return fmt.Sprintf("%04d_%s.up.sql", m.Version, m.Name)
}

// AppliedRecord is a row of the schema_migrations ledger.
type AppliedRecord struct {
	Version   int64
	Name      string
	Checksum  string
	Critical  bool
	AppliedAt time.Time
}

// Store is the narrow database surface the package needs. *pgxpool.Pool
// satisfies it directly.
type Store interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

// ensureLedgerSQL creates the migration ledger. It is idempotent and is also
// emitted by the baseline migration so the shape is visible in migrations/.
const ensureLedgerSQL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version     bigint PRIMARY KEY,
    name        text NOT NULL,
    checksum    text NOT NULL,
    critical    boolean NOT NULL DEFAULT true,
    applied_at  timestamptz NOT NULL DEFAULT now()
)`

// Load reads every migration in dir and validates the set. Both up and down
// files are required unless the up file opts out with
// "-- rs:reversible=false".
func Load(dir string) ([]Migration, error) {
	entries, err := fs.Glob(os.DirFS(dir), "*.sql")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir %q: %w", dir, err)
	}
	type pair struct {
		up   string
		down string
	}
	pairs := map[int64]*pair{}
	names := map[int64]string{}
	for _, entry := range entries {
		m := fileNamePattern.FindStringSubmatch(entry)
		if m == nil {
			return nil, fmt.Errorf("migration file %q does not match <version>_<name>.<up|down>.sql", entry)
		}
		version, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("migration file %q: %w", entry, err)
		}
		name := m[2]
		if existing, ok := names[version]; ok && existing != name {
			return nil, fmt.Errorf("migration version %d has conflicting names %q and %q", version, existing, name)
		}
		names[version] = name
		if pairs[version] == nil {
			pairs[version] = &pair{}
		}
		switch m[3] {
		case "up":
			pairs[version].up = entry
		case "down":
			pairs[version].down = entry
		}
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("no migrations found in %q", dir)
	}

	out := make([]Migration, 0, len(pairs))
	for version, p := range pairs {
		if p.up == "" {
			return nil, fmt.Errorf("migration version %d has a down file but no up file", version)
		}
		upSQL, upDirectives, err := readFile(dir, p.up)
		if err != nil {
			return nil, err
		}
		mg := Migration{
			Version:    version,
			Name:       names[version],
			UpSQL:      upSQL,
			Critical:   directiveBool(upDirectives, "critical", true),
			Reversible: directiveBool(upDirectives, "reversible", true),
			Checksum:   checksum(upSQL),
		}
		if p.down == "" {
			if mg.Reversible {
				return nil, fmt.Errorf("%w: %s", ErrNoDownMigration, p.up)
			}
		} else {
			downSQL, _, err := readFile(dir, p.down)
			if err != nil {
				return nil, err
			}
			mg.DownSQL = downSQL
		}
		out = append(out, mg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

func readFile(dir, name string) (string, map[string]string, error) {
	data, err := fs.ReadFile(os.DirFS(dir), name)
	if err != nil {
		return "", nil, fmt.Errorf("read migration %s: %w", name, err)
	}
	content := string(data)
	if strings.TrimSpace(content) == "" {
		return "", nil, fmt.Errorf("migration %s is empty", name)
	}
	return content, parseDirectives(content), nil
}

// parseDirectives extracts "-- rs:<key>=<value>" directives from the leading
// comment block of a migration file.
func parseDirectives(content string) map[string]string {
	directives := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(trimmed, "--") {
			break // directives only live in the header block
		}
		body := strings.TrimSpace(strings.TrimPrefix(trimmed, "--"))
		if !strings.HasPrefix(body, "rs:") {
			continue
		}
		body = strings.TrimPrefix(body, "rs:")
		kv := strings.SplitN(body, "=", 2)
		if len(kv) != 2 {
			continue
		}
		directives[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return directives
}

func directiveBool(directives map[string]string, key string, def bool) bool {
	v, ok := directives[key]
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "1":
		return true
	case "false", "no", "0":
		return false
	}
	return def
}

func checksum(upSQL string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(upSQL)))
	return hex.EncodeToString(sum[:])
}

// EnsureLedger creates the ledger table if it does not exist.
func EnsureLedger(ctx context.Context, s Store) error {
	if _, err := s.Exec(ctx, ensureLedgerSQL); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}
	return nil
}

// Applied returns the ledger rows keyed by version.
func Applied(ctx context.Context, s Store) (map[int64]AppliedRecord, error) {
	if err := EnsureLedger(ctx, s); err != nil {
		return nil, err
	}
	rows, err := s.Query(ctx, `SELECT version, name, checksum, critical, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("query schema_migrations: %w", err)
	}
	defer rows.Close()
	out := map[int64]AppliedRecord{}
	for rows.Next() {
		var rec AppliedRecord
		if err := rows.Scan(&rec.Version, &rec.Name, &rec.Checksum, &rec.Critical, &rec.AppliedAt); err != nil {
			return nil, fmt.Errorf("scan schema_migrations: %w", err)
		}
		out[rec.Version] = rec
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schema_migrations: %w", err)
	}
	return out, nil
}

// Pending returns migrations in dir that are not yet applied, ordered by
// version. It fails when an applied migration no longer matches its file.
func Pending(ctx context.Context, s Store, dir string) ([]Migration, error) {
	all, err := Load(dir)
	if err != nil {
		return nil, err
	}
	applied, err := Applied(ctx, s)
	if err != nil {
		return nil, err
	}
	var out []Migration
	for _, mg := range all {
		rec, ok := applied[mg.Version]
		if !ok {
			out = append(out, mg)
			continue
		}
		if rec.Checksum != mg.Checksum {
			return nil, fmt.Errorf("%w: version %d (%s)", ErrMigrationDrift, mg.Version, mg.Filename())
		}
	}
	return out, nil
}

// Up applies every pending migration, each in its own transaction, and returns
// the migrations it applied. It is safe to run on every boot.
func Up(ctx context.Context, s Store, dir string) ([]Migration, error) {
	pending, err := Pending(ctx, s, dir)
	if err != nil {
		return nil, err
	}
	for _, mg := range pending {
		if err := apply(ctx, s, mg); err != nil {
			return pending, err
		}
	}
	return pending, nil
}

func apply(ctx context.Context, s Store, mg Migration) error {
	tx, err := s.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", mg.Version, err)
	}
	defer rollback(ctx, tx) // no-op after a successful Commit

	if _, err := tx.Exec(ctx, mg.UpSQL); err != nil {
		return fmt.Errorf("apply migration %04d_%s: %w", mg.Version, mg.Name, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, name, checksum, critical) VALUES ($1, $2, $3, $4)`,
		mg.Version, mg.Name, mg.Checksum, mg.Critical); err != nil {
		return fmt.Errorf("record migration %04d_%s: %w", mg.Version, mg.Name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %04d_%s: %w", mg.Version, mg.Name, err)
	}
	return nil
}

// Down reverses at most steps migrations, newest first, each in its own
// transaction. It refuses to reverse a migration whose file no longer matches
// the recorded checksum.
func Down(ctx context.Context, s Store, dir string, steps int) ([]Migration, error) {
	if steps <= 0 {
		return nil, fmt.Errorf("steps must be positive; got %d", steps)
	}
	all, err := Load(dir)
	if err != nil {
		return nil, err
	}
	byVersion := map[int64]Migration{}
	for _, mg := range all {
		byVersion[mg.Version] = mg
	}
	applied, err := Applied(ctx, s)
	if err != nil {
		return nil, err
	}
	versions := make([]int64, 0, len(applied))
	for v := range applied {
		versions = append(versions, v)
	}
	sort.Sort(sort.Reverse(sortInt64(versions)))

	limit := len(versions)
	if limit > steps {
		limit = steps
	}
	reversed := make([]Migration, 0, limit)
	for _, v := range versions {
		if len(reversed) >= steps {
			break
		}
		rec := applied[v]
		mg, ok := byVersion[v]
		if !ok {
			return reversed, fmt.Errorf("applied migration %d (%s) is missing from %s", v, rec.Name, path.Base(dir))
		}
		if rec.Checksum != mg.Checksum {
			return reversed, fmt.Errorf("%w: version %d (%s)", ErrMigrationDrift, v, mg.Filename())
		}
		if strings.TrimSpace(mg.DownSQL) == "" {
			return reversed, fmt.Errorf("%w: %s", ErrNoDownMigration, mg.Filename())
		}
		if err := reverse(ctx, s, mg); err != nil {
			return reversed, err
		}
		reversed = append(reversed, mg)
	}
	return reversed, nil
}

func reverse(ctx context.Context, s Store, mg Migration) error {
	tx, err := s.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin rollback %d: %w", mg.Version, err)
	}
	defer rollback(ctx, tx)

	if _, err := tx.Exec(ctx, mg.DownSQL); err != nil {
		return fmt.Errorf("reverse migration %04d_%s: %w", mg.Version, mg.Name, err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, mg.Version); err != nil {
		return fmt.Errorf("unrecord migration %04d_%s: %w", mg.Version, mg.Name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rollback %04d_%s: %w", mg.Version, mg.Name, err)
	}
	return nil
}

// rollback ignores the expected ErrTxClosed after a successful commit.
func rollback(ctx context.Context, tx pgx.Tx) {
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		// Rollback failures after a failed statement are non-actionable.
		_ = err
	}
}

type sortInt64 []int64

func (s sortInt64) Len() int           { return len(s) }
func (s sortInt64) Less(i, j int) bool { return s[i] < s[j] }
func (s sortInt64) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

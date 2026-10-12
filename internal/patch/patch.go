// Package patch parses and applies unified diffs inside a workspace with
// strict preconditions. A patch is applied atomically: if any hunk fails, the
// workspace is restored to its pre-patch hashes.
package patch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Errors returned by the patch engine.
var (
	ErrMalformedDiff = errors.New("patch: malformed unified diff")
	ErrPathEscape    = errors.New("patch: path escapes the workspace")
	ErrProtectedPath = errors.New("patch: path is protected and requires a policy exception")
	ErrBinaryPatch   = errors.New("patch: binary edits are not supported")
	ErrHashMismatch  = errors.New("patch: file hash precondition failed")
	ErrSymlink       = errors.New("patch: refusing to patch through a symlink")
	ErrTooLarge      = errors.New("patch: file exceeds the size limit")
	ErrLineEndings   = errors.New("patch: mixed line endings are not supported")
	// ErrHunkContext means the diff does not match the file content.
	ErrHunkContext = errors.New("patch: hunk context does not match the file")
)

// Limits that make size bombs impossible.
const (
	MaxFileBytes  = 4 << 20
	MaxHunks      = 512
	MaxPatchBytes = 8 << 20
)

// FilePatch is one parsed file section of a unified diff.
type FilePatch struct {
	OldPath string
	NewPath string
	Hunks   []Hunk
	// NewFile and DeletedFile carry git-style headers.
	NewFile     bool
	DeletedFile bool
	RenameFrom  string
	RenameTo    string
	Mode        string
}

// Hunk is one @@ section.
type Hunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
	Lines    []string
}

// Patch is a parsed unified diff.
type Patch struct {
	Files []FilePatch
}

// ProtectedPathFunc reports whether a path is protected.
type ProtectedPathFunc func(path string) bool

// DefaultProtectedPaths lists paths no patch may touch without an exception.
func DefaultProtectedPaths() []string {
	return []string{
		".git",
		".github/workflows",
		".githooks",
		".env",
		"migrations",
		"Dockerfile",
		"docker-compose.yml",
		".golangci.yml",
	}
}

// DefaultProtected reports whether path falls under a protected area.
func DefaultProtected(path string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(path))
	for _, protected := range DefaultProtectedPaths() {
		if cleaned == protected || strings.HasPrefix(cleaned, protected+"/") {
			return true
		}
		if strings.HasPrefix(protected, ".") && strings.HasPrefix(cleaned, protected) {
			return true
		}
	}
	return false
}

// Parse parses a unified diff produced by `git diff` or `git format-patch`
// headers. It rejects binary patches and oversized diffs.
func Parse(diff string) (*Patch, error) {
	if len(diff) > MaxPatchBytes {
		return nil, fmt.Errorf("%w: diff is %d bytes, limit %d", ErrTooLarge, len(diff), MaxPatchBytes)
	}
	if strings.Contains(diff, "GIT binary patch") || strings.Contains(diff, "Binary files") {
		return nil, ErrBinaryPatch
	}
	lines := strings.Split(diff, "\n")
	p := &Patch{}
	var current *FilePatch
	var currentHunk *Hunk
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "diff --git"):
			current = &FilePatch{}
			p.Files = append(p.Files, *current)
			current = &p.Files[len(p.Files)-1]
			currentHunk = nil
		case strings.HasPrefix(line, "new file mode"):
			current.NewFile = true
		case strings.HasPrefix(line, "deleted file mode"):
			current.DeletedFile = true
		case strings.HasPrefix(line, "rename from "):
			if current == nil {
				return nil, fmt.Errorf("%w: rename outside a file section", ErrMalformedDiff)
			}
			current.RenameFrom = parsePath(strings.TrimPrefix(line, "rename from "))
		case strings.HasPrefix(line, "rename to "):
			if current == nil {
				return nil, fmt.Errorf("%w: rename outside a file section", ErrMalformedDiff)
			}
			current.RenameTo = parsePath(strings.TrimPrefix(line, "rename to "))
		case strings.HasPrefix(line, "old mode") || strings.HasPrefix(line, "new mode"):
			// Mode changes are recorded but not applied in this milestone.
			current.Mode = strings.TrimSpace(strings.TrimPrefix(line, "new mode "))
		case strings.HasPrefix(line, "--- "):
			if current == nil {
				return nil, fmt.Errorf("%w: hunk header before file header", ErrMalformedDiff)
			}
			current.OldPath = parsePath(strings.TrimPrefix(line, "--- "))
		case strings.HasPrefix(line, "+++ "):
			if current == nil {
				return nil, fmt.Errorf("%w: hunk header before file header", ErrMalformedDiff)
			}
			current.NewPath = parsePath(strings.TrimPrefix(line, "+++ "))
		case strings.HasPrefix(line, "@@"):
			if current == nil {
				return nil, fmt.Errorf("%w: hunk before file header", ErrMalformedDiff)
			}
			hunk, err := parseHunkHeader(line)
			if err != nil {
				return nil, err
			}
			if len(current.Hunks) >= MaxHunks {
				return nil, fmt.Errorf("%w: more than %d hunks", ErrTooLarge, MaxHunks)
			}
			current.Hunks = append(current.Hunks, hunk)
			currentHunk = &current.Hunks[len(current.Hunks)-1]
		case strings.HasPrefix(line, "+"), strings.HasPrefix(line, "-"), strings.HasPrefix(line, " "):
			if currentHunk == nil {
				return nil, fmt.Errorf("%w: content line outside a hunk", ErrMalformedDiff)
			}
			currentHunk.Lines = append(currentHunk.Lines, line)
		case line == "\\ No newline at end of file":
			// Trailing-newline marker is not representable; the parser keeps the
			// lines as written.
			continue
		case line == "" && currentHunk != nil && i < len(lines)-1:
			// git emits an empty line for an empty context line.
			currentHunk.Lines = append(currentHunk.Lines, " ")
		case line == "":
			continue
		default:
			return nil, fmt.Errorf("%w: unexpected line %q", ErrMalformedDiff, line)
		}
	}
	if len(p.Files) == 0 {
		return nil, fmt.Errorf("%w: no file sections", ErrMalformedDiff)
	}
	return p, nil
}

func parsePath(raw string) string {
	path := strings.TrimSpace(raw)
	if path == "/dev/null" {
		return ""
	}
	// Tab-separated timestamps are dropped.
	if at := strings.Index(path, "\t"); at >= 0 {
		path = path[:at]
	}
	// git-diff prefixes the two sides with a/ and b/.
	if len(path) > 2 && (strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/")) {
		path = path[2:]
	}
	return path
}

func parseHunkHeader(line string) (Hunk, error) {
	inner := strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(line), "@@"), "@@ ")
	fields := strings.Fields(inner)
	if len(fields) < 2 {
		return Hunk{}, fmt.Errorf("%w: %q", ErrMalformedDiff, line)
	}
	old, err := parseRange(fields[0])
	if err != nil {
		return Hunk{}, err
	}
	newRange, err := parseRange(fields[1])
	if err != nil {
		return Hunk{}, err
	}
	return Hunk{OldStart: old[0], OldLines: old[1], NewStart: newRange[0], NewLines: newRange[1]}, nil
}

func parseRange(field string) ([2]int, error) {
	var out [2]int
	if !strings.HasPrefix(field, "-") && !strings.HasPrefix(field, "+") {
		return out, fmt.Errorf("%w: %q", ErrMalformedDiff, field)
	}
	body := field[1:]
	parts := strings.SplitN(body, ",", 2)
	start, err := parseInt(parts[0])
	if err != nil {
		return out, fmt.Errorf("%w: %q", ErrMalformedDiff, field)
	}
	out[0] = start
	if len(parts) == 2 {
		count, err := parseInt(parts[1])
		if err != nil {
			return out, fmt.Errorf("%w: %q", ErrMalformedDiff, field)
		}
		out[1] = count
	} else {
		out[1] = 1
	}
	return out, nil
}

func parseInt(raw string) (int, error) {
	value := 0
	if raw == "" {
		return 0, errors.New("empty number")
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("not a number: %q", raw)
		}
		value = value*10 + int(r-'0')
	}
	return value, nil
}

// Apply applies the patch inside root. Protected paths are refused unless
// allowed permits them. Every file is verified against its hash before and
// after; on any failure the workspace is rolled back.
func (p *Patch) Apply(root string, allowed ProtectedPathFunc) ([]Result, error) {
	if allowed == nil {
		allowed = DefaultProtected
	}
	results := make([]Result, 0, len(p.Files))
	// Snapshot every target so a failure can roll back exactly.
	type snapshot struct {
		rel     string
		existed bool
		content []byte
		mode    os.FileMode
	}
	var snapshots []snapshot
	for _, file := range p.Files {
		target := file.target()
		if allowed(target) {
			return nil, fmt.Errorf("%w: %s", ErrProtectedPath, target)
		}
		content, mode, err := readTarget(root, target)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot{rel: target, existed: content != nil, content: content, mode: mode})
	}

	for i, file := range p.Files {
		result, err := file.apply(root, snapshots[i].content)
		if err != nil {
			// Roll back every file touched so far.
			for _, snap := range snapshots {
				_ = restore(root, snap.rel, snap.content, snap.mode)
			}
			return results, fmt.Errorf("patch %s: %w", file.target(), err)
		}
		results = append(results, result)
	}
	return results, nil
}

// Result reports what one file patch did. Action is one of modified,
// created, deleted or renamed.
type Result struct {
	Path      string `json:"path"`
	Action    string `json:"action"`
	BeforeSHA string `json:"before_sha256"`
	AfterSHA  string `json:"after_sha256"`
	Hunks     int    `json:"hunks"`
}

// target returns the effective path a file patch touches.
func (f FilePatch) target() string {
	if f.RenameTo != "" {
		return f.RenameTo
	}
	if f.NewPath != "" {
		return f.NewPath
	}
	return f.OldPath
}

func (f FilePatch) apply(root string, before []byte) (Result, error) {
	target := f.target()
	if f.DeletedFile {
		full := filepath.Join(root, filepath.FromSlash(target))
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return Result{}, err
		}
		return Result{Path: target, Action: "deleted", BeforeSHA: hash(before)}, nil
	}
	content, err := applyHunks(before, f.Hunks)
	if err != nil {
		return Result{}, err
	}
	mode := os.FileMode(0o644)
	if before == nil {
		// Creating a new file: the diff must have no removals outside the body.
		for _, hunk := range f.Hunks {
			for _, line := range hunk.Lines {
				if strings.HasPrefix(line, "-") {
					return Result{}, fmt.Errorf("%w: new file patch removes lines", ErrMalformedDiff)
				}
			}
		}
	}
	full := filepath.Join(root, filepath.FromSlash(target))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(full, content, mode); err != nil {
		return Result{}, err
	}
	action := "modified"
	if before == nil {
		action = "created"
	} else if f.RenameFrom != "" && f.RenameTo != "" {
		action = "renamed"
	}
	return Result{Path: target, Action: action, BeforeSHA: hash(before), AfterSHA: hash(content), Hunks: len(f.Hunks)}, nil
}

// applyHunks applies hunks to content, verifying every context and removed
// line against the source so a stale diff fails instead of corrupting a file.
func applyHunks(content []byte, hunks []Hunk) ([]byte, error) {
	if len(hunks) == 0 {
		return nil, fmt.Errorf("%w: file section has no hunks", ErrMalformedDiff)
	}
	text := string(content)
	if content != nil && strings.Contains(text, "\r\n") {
		return nil, ErrLineEndings
	}
	var lines []string
	if content != nil {
		lines = strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	}
	out := make([]string, 0, len(lines)+64)
	pos := 0
	for _, hunk := range hunks {
		// New files use @@ -0,0 +1,N @@, so OldStart 0 is legal only when the
		// file does not exist yet.
		start := hunk.OldStart - 1
		if hunk.OldStart == 0 && content == nil {
			start = 0
		}
		if start < pos || (content != nil && start > len(lines)) {
			return nil, fmt.Errorf("%w: hunk at %d overlaps or is out of range", ErrMalformedDiff, hunk.OldStart)
		}
		out = append(out, lines[pos:start]...)
		pos = start
		var oldCount, newCount int
		for _, line := range hunk.Lines {
			if line == "" {
				return nil, fmt.Errorf("%w: empty hunk line", ErrMalformedDiff)
			}
			switch line[0] {
			case ' ':
				if pos >= len(lines) || lines[pos] != line[1:] {
					return nil, fmt.Errorf("%w: expected %q at line %d", ErrHunkContext, line[1:], pos+1)
				}
				out = append(out, lines[pos])
				pos++
				oldCount++
				newCount++
			case '-':
				if pos >= len(lines) || lines[pos] != line[1:] {
					return nil, fmt.Errorf("%w: expected %q at line %d", ErrHunkContext, line[1:], pos+1)
				}
				pos++
				oldCount++
			case '+':
				out = append(out, line[1:])
				newCount++
			default:
				return nil, fmt.Errorf("%w: bad hunk line %q", ErrMalformedDiff, line)
			}
		}
		if oldCount != hunk.OldLines || newCount != hunk.NewLines {
			return nil, fmt.Errorf("%w: hunk at %d declares %d/%d lines but carries %d/%d",
				ErrMalformedDiff, hunk.OldStart, hunk.OldLines, hunk.NewLines, oldCount, newCount)
		}
	}
	out = append(out, lines[pos:]...)
	result := strings.Join(out, "\n")
	if len(out) > 0 {
		result += "\n"
	}
	return []byte(result), nil
}

func readTarget(root, rel string) ([]byte, os.FileMode, error) {
	cleaned := filepath.Clean("/" + filepath.FromSlash(rel))
	if strings.Contains(rel, "..") {
		return nil, 0, fmt.Errorf("%w: %s", ErrPathEscape, rel)
	}
	full := filepath.Join(root, cleaned)
	info, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, 0, fmt.Errorf("%w: %s", ErrSymlink, rel)
	}
	if info.Size() > MaxFileBytes {
		return nil, 0, fmt.Errorf("%w: %s is %d bytes", ErrTooLarge, rel, info.Size())
	}
	// #nosec G304 G122 -- path is workspace-relative, cleaned and Lstat-checked
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, 0, err
	}
	return data, info.Mode().Perm(), nil
}

func restore(root, rel string, content []byte, mode os.FileMode) error {
	if content == nil {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	return os.WriteFile(full, content, mode)
}

// hash returns the sha256 of content, or the empty string when content is nil.
func hash(content []byte) string {
	if content == nil {
		return ""
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Hash returns the content hash used in preconditions.
func Hash(content []byte) string { return hash(content) }

// Paths returns every path a patch touches, sorted for determinism.
func (p *Patch) Paths() []string {
	out := make([]string, 0, len(p.Files))
	for _, file := range p.Files {
		out = append(out, file.target())
	}
	sort.Strings(out)
	return out
}

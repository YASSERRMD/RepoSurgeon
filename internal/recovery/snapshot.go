package recovery

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Snapshot limits keep a checkpoint bounded so a runaway workspace cannot
// exhaust disk or memory during a repair loop.
const (
	// MaxSnapshotFiles bounds how many files one snapshot may hold.
	MaxSnapshotFiles = 20000
	// MaxSnapshotFileBytes bounds a single captured file.
	MaxSnapshotFileBytes = 8 << 20
	// MaxSnapshotBytes bounds the whole captured payload.
	MaxSnapshotBytes = 256 << 20
)

// FileState is one file inside a snapshot.
type FileState struct {
	// Content is the exact file content captured.
	Content []byte `json:"content"`
	// Mode is the permission bits recorded at capture time.
	Mode os.FileMode `json:"mode"`
	// Hash is the sha256 of Content, used to detect drift.
	Hash string `json:"hash"`
}

// Snapshot is a known-good workspace state. Unlike a hash-only checkpoint it
// holds content, so a rollback genuinely restores files instead of only
// reporting that something changed.
type Snapshot struct {
	// ID identifies the snapshot.
	ID string `json:"id"`
	// RunID is the owning run.
	RunID string `json:"run_id"`
	// WorkspaceID is the workspace the snapshot belongs to.
	WorkspaceID string `json:"workspace_id"`
	// Root is the absolute workspace root at capture time.
	Root string `json:"-"`
	// Files are the captured file states keyed by slash path.
	Files map[string]FileState `json:"files"`
	// Verdict is the check verdict this snapshot is known to hold.
	Verdict string `json:"verdict"`
	// CreatedAt is the RFC3339 timestamp.
	CreatedAt string `json:"created_at"`
}

// ErrSnapshotTooLarge reports that a workspace exceeded the snapshot budget.
var ErrSnapshotTooLarge = errors.New("recovery: workspace exceeds the snapshot budget")

// Capture records every file under root, skipping VCS and dependency
// directories whose content is reproducible.
func Capture(root, runID, workspaceID, verdict string) (Snapshot, error) {
	files := map[string]FileState{}
	total := 0
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", ".venv", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if len(files) >= MaxSnapshotFiles {
			return ErrSnapshotTooLarge
		}
		if info.Size() > MaxSnapshotFileBytes {
			return fmt.Errorf("%w: %s is %d bytes", ErrSnapshotTooLarge, path, info.Size())
		}
		content, readErr := os.ReadFile(path) // #nosec G304 G122 -- path is walked under the workspace root
		if readErr != nil {
			return readErr
		}
		total += len(content)
		if total > MaxSnapshotBytes {
			return fmt.Errorf("%w: captured %d bytes", ErrSnapshotTooLarge, total)
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		files[filepath.ToSlash(rel)] = FileState{Content: content, Mode: info.Mode().Perm(), Hash: checksHash(string(content))}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		ID:          NewSnapshotID(),
		RunID:       runID,
		WorkspaceID: workspaceID,
		Root:        root,
		Files:       files,
		Verdict:     verdict,
		CreatedAt:   nowRFC3339(),
	}, nil
}

// Drift returns the files whose current content differs from the snapshot.
func (s Snapshot) Drift(root string) ([]string, error) {
	var drifted []string
	for rel, state := range s.Files {
		current, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) // #nosec G304 G122 -- path stays inside the workspace root
		if err != nil {
			if os.IsNotExist(err) {
				drifted = append(drifted, rel)
				continue
			}
			return nil, err
		}
		if checksHash(string(current)) != state.Hash {
			drifted = append(drifted, rel)
		}
	}
	sort.Strings(drifted)
	return drifted, nil
}

// Restore rewrites the workspace to the captured state and returns the files
// that were changed back. Files absent from the snapshot are removed so the
// rollback is exact, not best-effort.
func (s Snapshot) Restore(root string) ([]string, error) {
	var restored []string
	for rel, state := range s.Files {
		target := filepath.Join(root, filepath.FromSlash(rel))
		current, err := os.ReadFile(target) // #nosec G304 G122 -- path stays inside the workspace root
		if err == nil {
			if checksHash(string(current)) == state.Hash {
				continue
			}
		} else if !os.IsNotExist(err) {
			return restored, err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return restored, err
		}
		if err := os.WriteFile(target, state.Content, state.Mode); err != nil { // #nosec G306 G122 -- workspace file, mode preserved from capture
			return restored, err
		}
		restored = append(restored, rel)
	}
	// Remove files the snapshot never recorded, so an untracked artifact from a
	// failed attempt cannot survive the rollback.
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", ".venv", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if _, ok := s.Files[filepath.ToSlash(rel)]; !ok {
			if removeErr := os.Remove(path); removeErr != nil {
				return removeErr
			}
			restored = append(restored, "removed:"+filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(restored)
	return restored, err
}

// NewSnapshotID returns a fresh snapshot identifier.
func NewSnapshotID() string { return "snp_" + randomSuffix() }

// NormalizePath keeps snapshot keys portable across operating systems.
func NormalizePath(path string) string {
	return strings.TrimPrefix(filepath.ToSlash(path), "./")
}

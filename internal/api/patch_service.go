package api

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/YASSERRMD/RepoSurgeon/internal/domain"
	"github.com/YASSERRMD/RepoSurgeon/internal/patch"
)

// NewPatchService builds the patch service over the patch engine.
func NewPatchService(engine *patch.Patch, checkpoints CheckpointStore, workspaceDir func(string) string) *PatchService {
	return &PatchService{
		Engine:       engine,
		Checkpoints:  checkpoints,
		Protected:    patch.DefaultProtected,
		WorkspaceDir: workspaceDir,
	}
}

// workspaceDiff returns the git diff and per-file hashes for a workspace. The
// root is confined to the workspace directory by patchWorkspaceRoot.
func (s *Server) workspaceDiff(root string) (string, map[string]string, error) {
	// The root is derived from a validated workspace id in patchWorkspaceRoot.
	cmd := exec.CommandContext(context.Background(), "git", "diff", "HEAD") // #nosec G703 G204 -- workspace-scoped root
	cmd.Dir = root                                                          // #nosec G703 -- validated workspace root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", nil, errWorkspaceDiffFailed
	}
	hashes, err := hashWorkspace(root)
	if err != nil {
		return "", nil, err
	}
	return string(out), hashes, nil
}

// hashWorkspace returns sha256 for every file under root, skipping .git.
func hashWorkspace(root string) (map[string]string, error) {
	out := map[string]string{}
	// The root is derived from a validated workspace id in patchWorkspaceRoot.
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error { // #nosec G703 G122 -- workspace-scoped root
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		data, readErr := os.ReadFile(path) // #nosec G304 G122 -- path is walked under the workspace root
		if readErr != nil {
			return nil
		}
		out[filepath.ToSlash(rel)] = patch.Hash(data)
		return nil
	})
	if err != nil {
		return nil, errWorkspaceDiffFailed
	}
	return out, nil
}

// memoryCheckpoints stores checkpoints in-process for M10.
type memoryCheckpoints struct {
	items map[string]Checkpoint
	order []string
}

// NewMemoryCheckpoints builds the in-memory checkpoint store.
func NewMemoryCheckpoints() CheckpointStore {
	return &memoryCheckpoints{items: map[string]Checkpoint{}}
}

func (m *memoryCheckpoints) Save(_ context.Context, checkpoint Checkpoint) error {
	if checkpoint.ID == "" {
		return errCheckpointInvalid
	}
	if _, exists := m.items[checkpoint.ID]; !exists {
		m.order = append(m.order, checkpoint.ID)
	}
	m.items[checkpoint.ID] = checkpoint
	return nil
}

func (m *memoryCheckpoints) Get(_ context.Context, checkpointID string) (Checkpoint, bool, error) {
	checkpoint, ok := m.items[checkpointID]
	if !ok {
		return Checkpoint{}, false, nil
	}
	return checkpoint, true, nil
}

var (
	errWorkspaceDiffFailed = errWorkspace("could not read the workspace diff")
	errCheckpointInvalid   = errWorkspace("checkpoint id is required")
)

type errWorkspace string

func (e errWorkspace) Error() string { return string(e) }

var _ = strings.TrimSpace
var _ = domain.NewCheckpointID

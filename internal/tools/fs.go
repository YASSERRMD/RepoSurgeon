package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// statPath wraps os.Stat so the executor stays testable.
func statPath(ctx context.Context, path string) (fileInfo, error) {
	if err := ctx.Err(); err != nil {
		return fileInfo{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fileInfo{}, err
	}
	return fileInfo{info}, nil
}

func readFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

type fileInfo struct{ os.FileInfo }

func (f fileInfo) IsDir() bool { return f.FileInfo.IsDir() }

func (f fileInfo) Size() int64 { return f.FileInfo.Size() }

// searchFiles walks root and returns matches whose relative path or content
// contains pattern. Hidden directories are skipped.
func searchFiles(root, pattern string, limit int) ([]map[string]any, error) {
	if strings.TrimSpace(pattern) == "" {
		return nil, errPatternRequired
	}
	var matches []map[string]any
	// The root is validated by Executor.safePath before the walk starts.
	// #nosec G122 -- root is canonicalised and symlink-resolved by safePath.
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" {
				return filepath.SkipDir
			}
			if strings.HasPrefix(entry.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if len(matches) >= limit {
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if strings.Contains(rel, pattern) {
			matches = append(matches, map[string]any{"path": rel, "line": 0, "preview": ""})
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil || info.Size() > maxFileBytes {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		for i, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, pattern) {
				matches = append(matches, map[string]any{
					"path":    rel,
					"line":    i + 1,
					"preview": truncate(line, 200),
				})
				break
			}
			if len(matches) >= limit {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "…"
}

var errPatternRequired = errPatternRequiredType{}

type errPatternRequiredType struct{}

func (errPatternRequiredType) Error() string { return "tools: pattern is required" }

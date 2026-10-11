package index

import (
	"os"
	"path/filepath"

	"github.com/YASSERRMD/RepoSurgeon/internal/gitworkspace"
)

// FileEntry is one directory entry for the tree endpoint.
type FileEntry = gitworkspace.FileEntry

// Exists reports whether path is an existing directory.
func Exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// ListTree lists the files under root without the .git directory, reusing the
// shared walker so tree and search cannot diverge.
func ListTree(root string) ([]FileEntry, error) {
	entries, err := gitworkspace.ListFiles(root)
	if err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, FileEntry{
			Path:  filepath.ToSlash(entry.Path),
			IsDir: entry.IsDir,
			Size:  entry.Size,
		})
	}
	return out, nil
}

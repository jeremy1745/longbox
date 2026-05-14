package scanner

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jeremy/longbox/internal/archive"
)

// Result represents a discovered comic file.
type Result struct {
	Path     string
	Name     string
	Size     int64
	Format   string
}

// Scan walks the given directory and returns all comic files found.
func Scan(dir string) ([]Result, error) {
	var results []Result

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			slog.Warn("error accessing path", "path", path, "error", err)
			return nil // skip errors and continue
		}

		if d.IsDir() {
			// Skip hidden directories
			if strings.HasPrefix(d.Name(), ".") && path != dir {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip symlinks to prevent directory traversal
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		if !archive.IsComicFile(path) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			slog.Warn("error reading file info", "path", path, "error", err)
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		format := strings.TrimPrefix(ext, ".")

		results = append(results, Result{
			Path:   path,
			Name:   d.Name(),
			Size:   info.Size(),
			Format: format,
		})

		return nil
	})

	if err != nil {
		return nil, err
	}

	slog.Info("scan complete", "files_found", len(results), "directory", dir)
	return results, nil
}

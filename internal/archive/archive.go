package archive

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// maxExtractSize is the maximum decompressed size for a single file extraction (100 MB).
const maxExtractSize = 100 << 20

// isSafeEntryName returns true if the archive entry name doesn't contain path traversal.
func isSafeEntryName(name string) bool {
	if name == "" {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(name))
	return !strings.HasPrefix(cleaned, "..") && !filepath.IsAbs(cleaned)
}

// Entry represents a file inside a comic archive.
type Entry struct {
	Name string
	Size int64
}

// Archive provides read access to a comic archive.
type Archive interface {
	// ListEntries returns all entries in the archive sorted by name.
	ListEntries() ([]Entry, error)
	// ExtractFile extracts a single file by name from the archive.
	ExtractFile(name string) (io.ReadCloser, error)
	// Close releases any resources held by the archive.
	Close() error
}

// Open opens a comic archive based on file extension.
func Open(path string) (Archive, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".cbz", ".zip":
		return OpenCBZ(path)
	case ".cbr", ".rar":
		return OpenCBR(path)
	case ".cb7", ".7z":
		return OpenCB7(path)
	default:
		return nil, fmt.Errorf("unsupported archive format: %s", ext)
	}
}

// imageExtensions lists file extensions considered as comic page images.
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true,
	".webp": true, ".gif": true, ".bmp": true,
}

// IsImageFile returns true if the filename has an image extension.
func IsImageFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return imageExtensions[ext]
}

// skipPrefixes lists filename prefixes commonly used for non-cover images.
var skipPrefixes = []string{"z_", "zz_", "zzz_", "credits", "z-"}

// FindCoverEntry finds the most likely cover image entry from a list of entries.
// It picks the first image file alphabetically, skipping known non-cover files.
func FindCoverEntry(entries []Entry) *Entry {
	var images []Entry
	for _, e := range entries {
		if !IsImageFile(e.Name) {
			continue
		}
		base := strings.ToLower(filepath.Base(e.Name))
		skip := false
		for _, prefix := range skipPrefixes {
			if strings.HasPrefix(base, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			images = append(images, e)
		}
	}

	if len(images) == 0 {
		return nil
	}

	sort.Slice(images, func(i, j int) bool {
		return images[i].Name < images[j].Name
	})

	return &images[0]
}

// IsComicFile returns true if the file path has a supported comic extension.
func IsComicFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".cbz", ".cbr", ".cb7", ".pdf":
		return true
	}
	return false
}

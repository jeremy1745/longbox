package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// WriteComicInfoToCBZ rewrites a CBZ file with an updated (or new) ComicInfo.xml.
// It creates a temporary file, copies all existing entries (replacing or adding
// ComicInfo.xml), then atomically renames the temp file over the original.
// Only CBZ/ZIP format is supported for writing.
func WriteComicInfoToCBZ(archivePath string, info *ComicInfo) error {
	ext := strings.ToLower(filepath.Ext(archivePath))
	if ext != ".cbz" && ext != ".zip" {
		return fmt.Errorf("writing ComicInfo.xml is only supported for CBZ files, got %s", ext)
	}

	xmlData, err := info.MarshalToXML()
	if err != nil {
		return err
	}

	// Open the original archive for reading
	src, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("opening source archive: %w", err)
	}
	defer src.Close()

	// Create temp file in the same directory (for atomic rename)
	dir := filepath.Dir(archivePath)
	tmp, err := os.CreateTemp(dir, ".longbox-rewrite-*.cbz")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		// Clean up temp file on error
		if tmp != nil {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()

	dst := zip.NewWriter(tmp)

	// Write ComicInfo.xml first
	ciWriter, err := dst.Create("ComicInfo.xml")
	if err != nil {
		return fmt.Errorf("creating ComicInfo.xml entry: %w", err)
	}
	if _, err := ciWriter.Write(xmlData); err != nil {
		return fmt.Errorf("writing ComicInfo.xml: %w", err)
	}

	// Copy all other entries from original, skipping any existing ComicInfo.xml
	for _, f := range src.File {
		if strings.EqualFold(f.Name, "ComicInfo.xml") ||
			strings.HasSuffix(strings.ToLower(f.Name), "/comicinfo.xml") {
			continue // Skip — we already wrote the new one
		}

		// Preserve the original file's metadata
		header := f.FileHeader
		w, err := dst.CreateHeader(&header)
		if err != nil {
			return fmt.Errorf("creating entry %s: %w", f.Name, err)
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("opening entry %s: %w", f.Name, err)
		}

		if _, err := io.Copy(w, rc); err != nil {
			rc.Close()
			return fmt.Errorf("copying entry %s: %w", f.Name, err)
		}
		rc.Close()
	}

	if err := dst.Close(); err != nil {
		return fmt.Errorf("finalizing archive: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	// Atomic rename: replace original with rewritten file
	if err := os.Rename(tmpPath, archivePath); err != nil {
		return fmt.Errorf("replacing original file: %w", err)
	}

	// Prevent deferred cleanup from removing the successfully renamed file
	tmp = nil
	return nil
}

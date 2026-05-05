package archive

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ConvertToCBZ reads any supported comic archive (CBR, CB7, CBZ) and produces
// a new CBZ at outputPath containing the same entries plus an embedded
// ComicInfo.xml. The original is left untouched — caller decides what to do
// with it.
//
// If srcPath and outputPath resolve to the same on-disk file, it just rewrites
// the existing CBZ via WriteComicInfoToCBZ. Otherwise outputPath must not exist
// already (caller's responsibility to check).
func ConvertToCBZ(srcPath, outputPath string, info *ComicInfo) error {
	srcAbs, _ := filepath.Abs(srcPath)
	dstAbs, _ := filepath.Abs(outputPath)
	if srcAbs == dstAbs {
		return WriteComicInfoToCBZ(srcPath, info)
	}

	src, err := Open(srcPath)
	if err != nil {
		return fmt.Errorf("opening source archive: %w", err)
	}
	defer src.Close()

	entries, err := src.ListEntries()
	if err != nil {
		return fmt.Errorf("listing source entries: %w", err)
	}

	xmlData, err := info.MarshalToXML()
	if err != nil {
		return err
	}

	dir := filepath.Dir(outputPath)
	tmp, err := os.CreateTemp(dir, ".longbox-convert-*.cbz")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			tmp.Close()
			os.Remove(tmpPath)
		}
	}()

	dst := zip.NewWriter(tmp)

	ciWriter, err := dst.Create("ComicInfo.xml")
	if err != nil {
		return fmt.Errorf("creating ComicInfo.xml entry: %w", err)
	}
	if _, err := ciWriter.Write(xmlData); err != nil {
		return fmt.Errorf("writing ComicInfo.xml: %w", err)
	}

	for _, e := range entries {
		// Skip any existing ComicInfo.xml — we already wrote the new one above.
		if strings.EqualFold(e.Name, "ComicInfo.xml") ||
			strings.HasSuffix(strings.ToLower(e.Name), "/comicinfo.xml") {
			continue
		}

		w, err := dst.Create(e.Name)
		if err != nil {
			return fmt.Errorf("creating entry %q: %w", e.Name, err)
		}
		rc, err := src.ExtractFile(e.Name)
		if err != nil {
			return fmt.Errorf("extracting entry %q: %w", e.Name, err)
		}
		if _, err := io.Copy(w, rc); err != nil {
			rc.Close()
			return fmt.Errorf("copying entry %q: %w", e.Name, err)
		}
		rc.Close()
	}

	if err := dst.Close(); err != nil {
		return fmt.Errorf("finalizing zip: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp: %w", err)
	}

	if _, err := os.Stat(outputPath); err == nil {
		return fmt.Errorf("destination already exists: %s", outputPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stating destination: %w", err)
	}

	if err := os.Rename(tmpPath, outputPath); err != nil {
		return fmt.Errorf("renaming temp to destination: %w", err)
	}
	cleanup = false
	return nil
}

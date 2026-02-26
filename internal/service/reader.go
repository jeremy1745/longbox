package service

import (
	"fmt"
	"io"
	"mime"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jeremy/longbox/internal/archive"
)

// PageInfo describes a single page in a comic archive.
type PageInfo struct {
	Index int    `json:"index"`
	Name  string `json:"name"`
	Size  int64  `json:"size"`
}

// ReaderService handles page listing and extraction from comic archives.
type ReaderService struct{}

// NewReaderService creates a new reader service.
func NewReaderService() *ReaderService {
	return &ReaderService{}
}

// ListPages opens the archive at archivePath, filters to image files,
// sorts alphabetically, and returns the list with 0-based indices.
func (s *ReaderService) ListPages(archivePath string) ([]PageInfo, error) {
	a, err := archive.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("opening archive: %w", err)
	}
	defer a.Close()

	entries, err := a.ListEntries()
	if err != nil {
		return nil, fmt.Errorf("listing entries: %w", err)
	}

	// Filter to images only
	var images []archive.Entry
	for _, e := range entries {
		if archive.IsImageFile(e.Name) {
			images = append(images, e)
		}
	}

	// Sort alphabetically by name
	sort.Slice(images, func(i, j int) bool {
		return images[i].Name < images[j].Name
	})

	pages := make([]PageInfo, len(images))
	for i, img := range images {
		pages[i] = PageInfo{
			Index: i,
			Name:  img.Name,
			Size:  img.Size,
		}
	}

	return pages, nil
}

// ExtractPage opens the archive, finds the page at the given 0-based index,
// and returns the image data as an io.ReadCloser plus its MIME type.
// The caller MUST close the returned ReadCloser.
func (s *ReaderService) ExtractPage(archivePath string, pageIndex int) (io.ReadCloser, string, error) {
	a, err := archive.Open(archivePath)
	if err != nil {
		return nil, "", fmt.Errorf("opening archive: %w", err)
	}

	entries, err := a.ListEntries()
	if err != nil {
		a.Close()
		return nil, "", fmt.Errorf("listing entries: %w", err)
	}

	var images []archive.Entry
	for _, e := range entries {
		if archive.IsImageFile(e.Name) {
			images = append(images, e)
		}
	}

	sort.Slice(images, func(i, j int) bool {
		return images[i].Name < images[j].Name
	})

	if pageIndex < 0 || pageIndex >= len(images) {
		a.Close()
		return nil, "", fmt.Errorf("page index %d out of range (0-%d)", pageIndex, len(images)-1)
	}

	target := images[pageIndex]
	rc, err := a.ExtractFile(target.Name)
	if err != nil {
		a.Close()
		return nil, "", fmt.Errorf("extracting page: %w", err)
	}

	// Determine MIME type from extension
	ext := strings.ToLower(filepath.Ext(target.Name))
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Wrap the ReadCloser to also close the archive when the reader is closed.
	return &archivePageReader{reader: rc, archive: a}, mimeType, nil
}

// archivePageReader wraps a page ReadCloser and ensures the archive is closed too.
type archivePageReader struct {
	reader  io.ReadCloser
	archive archive.Archive
}

func (r *archivePageReader) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *archivePageReader) Close() error {
	err1 := r.reader.Close()
	err2 := r.archive.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

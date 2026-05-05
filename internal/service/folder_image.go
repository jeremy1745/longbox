package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
)

// folderImageFilenames are the on-disk names FolderImageService writes.
// folder.jpg is Windows' native folder thumbnail; cover.jpg is what most
// cross-platform comic tools (Komga, Plex, Mylar) look for.
var folderImageFilenames = []string{"folder.jpg", "cover.jpg"}

// FolderImageOutcome describes the result of refreshing a single series.
type FolderImageOutcome int

const (
	FolderImageWritten FolderImageOutcome = iota
	FolderImageUnchanged
	FolderImageSkippedNoSource
	FolderImageSkippedNoFolder
	FolderImageSkippedNoFiles
	FolderImageFailed
)

// FolderImageService is the "make every series have a usable poster" job.
//
// For each series it does, in order:
//  1. If cover_file_id is null and the series has comic files on disk,
//     extract the first file's cover thumbnail and set cover_file_id
//     (the UI's primary cover source).
//  2. If cover_image_url is empty and the series has a CV / Metron match,
//     call MetadataService.BackfillSeriesCoverURL to fetch and persist a
//     remote cover URL (the UI's fallback when no file is available).
//  3. If a cover source exists (file thumbnail or remote URL), drop a
//     folder.jpg + cover.jpg into the series folder so external scanners
//     (Plex, Komga, Windows Explorer) get a poster too.
//
// Step 2 is the only one that calls a provider API; quota errors are logged
// and the series is skipped — the job keeps moving.
type FolderImageService struct {
	seriesRepo *repository.SeriesRepo
	fileRepo   *repository.FileRepo
	librarySvc *LibraryService
	coverSvc   *CoverService
	metaSvc    *MetadataService
	http       *http.Client
}

func NewFolderImageService(
	seriesRepo *repository.SeriesRepo,
	fileRepo *repository.FileRepo,
	librarySvc *LibraryService,
	coverSvc *CoverService,
	metaSvc *MetadataService,
) *FolderImageService {
	return &FolderImageService{
		seriesRepo: seriesRepo,
		fileRepo:   fileRepo,
		librarySvc: librarySvc,
		coverSvc:   coverSvc,
		metaSvc:    metaSvc,
		http:       &http.Client{Timeout: 30 * time.Second},
	}
}

// WriteAll iterates every series and refreshes its poster sources, then drops
// folder.jpg / cover.jpg into the on-disk series folder.
func (s *FolderImageService) WriteAll(
	ctx context.Context,
	force bool,
	progress func(processed, total int, message string),
) (written, unchanged, skipped, failed int, err error) {
	if progress == nil {
		progress = func(int, int, string) {}
	}
	all, _, err := s.seriesRepo.List(1, 100000, "", "")
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("listing series: %w", err)
	}

	// Bulk prefetch files keyed by series_id. Avoids one ListBySeries
	// per series — single scan + in-memory index instead of N round-trips.
	progress(0, len(all), "Loading files (bulk prefetch)")
	filesBySeries, err := s.fileRepo.ListAllGroupedBySeries()
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("prefetching files: %w", err)
	}

	libraryDir := s.libraryDirOrEmpty()
	total := len(all)
	for i, sr := range all {
		select {
		case <-ctx.Done():
			return written, unchanged, skipped, failed, ctx.Err()
		default:
		}

		progress(i, total, fmt.Sprintf("Refreshing poster for %s (%d/%d)", sr.Title, i+1, total))

		switch s.refreshSeriesWithPrefetch(ctx, sr.ID, libraryDir, force, filesBySeries[sr.ID]) {
		case FolderImageWritten:
			written++
		case FolderImageUnchanged:
			unchanged++
		case FolderImageSkippedNoSource, FolderImageSkippedNoFolder, FolderImageSkippedNoFiles:
			skipped++
		case FolderImageFailed:
			failed++
		}
	}

	progress(total, total, fmt.Sprintf("Done: %d written, %d unchanged, %d skipped, %d failed",
		written, unchanged, skipped, failed))
	return written, unchanged, skipped, failed, nil
}

// WriteForSeries refreshes a single series synchronously.
func (s *FolderImageService) WriteForSeries(ctx context.Context, seriesID int64, force bool) (FolderImageOutcome, string, error) {
	libraryDir := s.libraryDirOrEmpty()
	files, err := s.fileRepo.ListBySeries(seriesID)
	if err != nil {
		return FolderImageFailed, "", fmt.Errorf("listing files: %w", err)
	}
	dir := ""
	if len(files) > 0 {
		dir = determineSeriesFolder(files, libraryDir)
	}
	return s.refreshSeries(ctx, seriesID, libraryDir, force), dir, nil
}

func (s *FolderImageService) libraryDirOrEmpty() string {
	if s.librarySvc == nil {
		return ""
	}
	return s.librarySvc.GetLibraryDir()
}

// refreshSeries is the per-series single-call entry point. Loads files on
// demand and delegates to refreshSeriesWithPrefetch.
func (s *FolderImageService) refreshSeries(ctx context.Context, seriesID int64, libraryDir string, force bool) FolderImageOutcome {
	files, err := s.fileRepo.ListBySeries(seriesID)
	if err != nil {
		slog.Warn("poster-refresh: list files failed", "series_id", seriesID, "error", err)
		return FolderImageFailed
	}
	return s.refreshSeriesWithPrefetch(ctx, seriesID, libraryDir, force, files)
}

// refreshSeriesWithPrefetch is the bulk-job entry point — `files` is
// supplied by WriteAll's bulk prefetch. Reloads the series row after each
// backfill step so subsequent steps see up-to-date fields.
func (s *FolderImageService) refreshSeriesWithPrefetch(ctx context.Context, seriesID int64, libraryDir string, force bool, files []model.ComicFile) FolderImageOutcome {
	sr, err := s.seriesRepo.GetByID(seriesID)
	if err != nil || sr == nil {
		slog.Warn("poster-refresh: series load failed", "series_id", seriesID, "error", err)
		return FolderImageFailed
	}

	// Step 1: extract first file's cover thumbnail if cover_file_id is missing
	// and the series has any files. Cheap, no API.
	if sr.CoverFileID == nil && len(files) > 0 {
		if firstFileID := s.extractFirstFileCover(files); firstFileID != 0 {
			if err := s.seriesRepo.UpdateCoverFileID(seriesID, firstFileID); err != nil {
				slog.Warn("poster-refresh: update cover_file_id failed",
					"series_id", seriesID, "error", err)
			} else {
				if reloaded, _ := s.seriesRepo.GetByID(seriesID); reloaded != nil {
					sr = reloaded
				}
			}
		}
	}

	// Step 2: backfill cover_image_url for matched-but-coverless series.
	// Provider API call — fails are logged and treated as non-fatal.
	if sr.CoverImageURL == "" && (sr.ComicVineID != nil || sr.MetronID != nil) {
		if err := s.metaSvc.BackfillSeriesCoverURL(ctx, seriesID); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return FolderImageFailed
			}
			slog.Info("poster-refresh: cover URL backfill failed (continuing)",
				"series_id", seriesID, "title", sr.Title, "error", err)
		} else {
			if reloaded, _ := s.seriesRepo.GetByID(seriesID); reloaded != nil {
				sr = reloaded
			}
		}
	}

	// Step 3: write folder.jpg / cover.jpg on disk. Mylar parity — every
	// series gets its own directory under the library root with a
	// cover image at the root of that directory.
	//
	// Folder selection (in order):
	//   - File-bearing series with a clean common-ancestor → use that
	//     folder (preserves the user's existing layout).
	//   - File-bearing series with files split across the library root
	//     → fall back to <libraryDir>/<seriesFolderName>; the cover
	//     lives in a predictable location and the next scan/organize
	//     run can consolidate the files there.
	//   - Tracked-empty series (no files yet) → create
	//     <libraryDir>/<seriesFolderName> so the on-disk catalog is
	//     "well organized" — every series visible as a folder with a
	//     poster, even before any issues are downloaded.
	seriesDir := ""
	if len(files) > 0 {
		seriesDir = determineSeriesFolder(files, libraryDir)
	}
	if seriesDir == "" {
		if libraryDir == "" {
			return FolderImageSkippedNoFolder
		}
		seriesDir = filepath.Join(libraryDir, seriesFolderName(sr))
		if err := os.MkdirAll(seriesDir, 0755); err != nil {
			slog.Warn("poster-refresh: mkdir failed",
				"series_id", sr.ID, "path", seriesDir, "error", err)
			return FolderImageFailed
		}
	}

	imgData, source, err := s.fetchCoverBytes(ctx, sr, files)
	if err != nil {
		slog.Warn("poster-refresh: fetch cover failed",
			"series_id", sr.ID, "title", sr.Title, "error", err)
		return FolderImageFailed
	}
	if len(imgData) == 0 {
		return FolderImageSkippedNoSource
	}

	anyWritten := false
	anyFailed := false
	for _, name := range folderImageFilenames {
		dest := filepath.Join(seriesDir, name)
		changed, err := writeImageIfChanged(dest, imgData, force)
		if err != nil {
			anyFailed = true
			slog.Warn("poster-refresh: disk write failed",
				"series_id", sr.ID, "path", dest, "error", err)
			continue
		}
		if changed {
			anyWritten = true
		}
	}

	if anyFailed && !anyWritten {
		return FolderImageFailed
	}
	if anyWritten {
		slog.Info("poster-refresh: wrote folder image",
			"series_id", sr.ID, "title", sr.Title,
			"folder", seriesDir, "source", source)
		return FolderImageWritten
	}
	return FolderImageUnchanged
}

// seriesFolderName produces a Mylar-style directory name for a series:
// "Series Title (Year)" with characters disallowed by Windows / NTFS
// stripped. Used when LongBox needs to materialize a series folder under
// the library root — e.g. for a tracked-but-empty series so the user
// gets a visible "shelf" with a poster image at its root.
//
// Falls back to "Series Title" when no year is set, and to a digest of
// the series ID if the title is empty (defensive — shouldn't happen for
// any real row).
func seriesFolderName(s *model.Series) string {
	title := strings.TrimSpace(s.Title)
	if title == "" {
		return fmt.Sprintf("series-%d", s.ID)
	}
	if s.Year != nil && *s.Year > 0 {
		title = fmt.Sprintf("%s (%d)", title, *s.Year)
	}
	return sanitizeFolderName(title)
}

// sanitizeFolderName strips characters that Windows / SMB / NTFS reject
// from a directory name and trims trailing whitespace + dots which
// Explorer silently drops on creation.
func sanitizeFolderName(name string) string {
	const banned = `<>:"/\|?*`
	out := make([]rune, 0, len(name))
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(banned, r) {
			out = append(out, ' ')
			continue
		}
		out = append(out, r)
	}
	collapsed := strings.Join(strings.Fields(string(out)), " ")
	return strings.TrimRight(collapsed, " .")
}

// extractFirstFileCover extracts the cover thumbnail for the first file that
// doesn't already have a cover_path. Returns the file ID whose cover_file_id
// the series should adopt (preferring an already-extracted thumbnail when one
// exists). Returns 0 if no file produced a usable cover.
func (s *FolderImageService) extractFirstFileCover(files []model.ComicFile) int64 {
	// Prefer a file that already has an extracted thumbnail — adopting it is
	// free.
	for _, f := range files {
		if f.CoverPath != "" {
			return f.ID
		}
	}
	// Fall back to extracting the first file's cover.
	for _, f := range files {
		if _, err := s.coverSvc.ExtractCover(f.ID, f.FilePath); err != nil {
			slog.Debug("poster-refresh: cover extract failed",
				"file_id", f.ID, "path", f.FilePath, "error", err)
			continue
		}
		return f.ID
	}
	return 0
}

// fetchCoverBytes returns the cover image bytes plus a short label for the source.
// Tries the file thumbnail first (cover_file_id → cover_path), then cover_image_url.
func (s *FolderImageService) fetchCoverBytes(ctx context.Context, sr *model.Series, files []model.ComicFile) ([]byte, string, error) {
	if sr.CoverFileID != nil {
		for _, f := range files {
			if f.ID == *sr.CoverFileID && f.CoverPath != "" {
				if data, err := os.ReadFile(f.CoverPath); err == nil {
					return data, "thumbnail", nil
				}
			}
		}
	}
	if sr.CoverImageURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, sr.CoverImageURL, nil)
		if err != nil {
			return nil, "", fmt.Errorf("building request: %w", err)
		}
		req.Header.Set("User-Agent", "longbox/1.0")
		resp, err := s.http.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("downloading cover: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("cover URL returned %d", resp.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		if err != nil {
			return nil, "", fmt.Errorf("reading cover body: %w", err)
		}
		return data, "url", nil
	}
	for _, f := range files {
		if f.CoverPath == "" {
			continue
		}
		if data, err := os.ReadFile(f.CoverPath); err == nil {
			return data, "fallback-thumbnail", nil
		}
	}
	return nil, "", nil
}

// writeImageIfChanged writes data to dest, skipping when an existing file
// already has identical bytes (unless force is true). Returns whether the
// file on disk changed.
func writeImageIfChanged(dest string, data []byte, force bool) (bool, error) {
	if !force {
		if existing, err := os.ReadFile(dest); err == nil {
			if sha256sum(existing) == sha256sum(data) {
				return false, nil
			}
		} else if err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("reading existing: %w", err)
		}
	}

	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, filepath.Base(dest)+"-*.tmp")
	if err != nil {
		return false, fmt.Errorf("temp file: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return false, fmt.Errorf("write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return false, fmt.Errorf("close temp: %w", err)
	}

	// Windows can't os.Rename over an existing file.
	if _, err := os.Stat(dest); err == nil {
		if err := os.Remove(dest); err != nil {
			os.Remove(tmpPath)
			return false, fmt.Errorf("remove existing: %w", err)
		}
	} else if !os.IsNotExist(err) {
		os.Remove(tmpPath)
		return false, fmt.Errorf("stat existing: %w", err)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		os.Remove(tmpPath)
		return false, fmt.Errorf("rename: %w", err)
	}
	return true, nil
}

func sha256sum(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

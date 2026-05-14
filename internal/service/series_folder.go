package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/template"
)

// SeriesFolderService creates a top-level library folder for a tracked
// series on disk and drops a folder.jpg (+ cover.jpg) poster fetched from
// the first issue's cover URL. Invoked by the calendar "Want" handler so
// that the moment a user tracks a series, its home in the library is
// visible and visually identifiable — before any issue file has landed.
type SeriesFolderService struct {
	librarySvc *LibraryService
	seriesRepo *repository.SeriesRepo
	issueRepo  *repository.IssueRepo
	http       *http.Client
}

func NewSeriesFolderService(librarySvc *LibraryService, seriesRepo *repository.SeriesRepo, issueRepo *repository.IssueRepo) *SeriesFolderService {
	return &SeriesFolderService{
		librarySvc: librarySvc,
		seriesRepo: seriesRepo,
		issueRepo:  issueRepo,
		http:       &http.Client{Timeout: 30 * time.Second},
	}
}

// EnsureFolderAndPoster creates the canonical series folder and writes
// folder.jpg / cover.jpg poster files. Idempotent: if folder.jpg already
// exists the call is a no-op. If no cover URL is available the folder is
// still created and the call returns nil (the poster simply doesn't get
// written yet).
//
// Folder naming uses the same SanitizePathComponent helper as the file
// organizer so the path is consistent with the eventual issue file
// layout — drops `<library>/<Series> (<Year>)/`.
func (s *SeriesFolderService) EnsureFolderAndPoster(ctx context.Context, seriesID int64) error {
	series, err := s.seriesRepo.GetByID(seriesID)
	if err != nil {
		return fmt.Errorf("loading series: %w", err)
	}
	if series == nil {
		return fmt.Errorf("series %d not found", seriesID)
	}

	folderName := buildSeriesFolderName(series.Title, series.Year)
	if folderName == "" {
		return fmt.Errorf("series has no usable title for folder name")
	}
	folderPath := filepath.Join(s.librarySvc.GetLibraryDir(), folderName)

	if err := os.MkdirAll(folderPath, 0o755); err != nil {
		return fmt.Errorf("creating series folder %q: %w", folderPath, err)
	}

	posterPath := filepath.Join(folderPath, "folder.jpg")
	coverAlias := filepath.Join(folderPath, "cover.jpg")

	if _, err := os.Stat(posterPath); err == nil {
		// Already have a poster — also make sure cover.jpg exists as a
		// Plex-friendly alias, but don't re-download.
		if _, err := os.Stat(coverAlias); err != nil {
			if data, rerr := os.ReadFile(posterPath); rerr == nil {
				_ = os.WriteFile(coverAlias, data, 0o644)
			}
		}
		return nil
	}

	// Find a usable cover URL — first issue with non-empty cover_url.
	issues, err := s.issueRepo.ListBySeries(seriesID)
	if err != nil {
		return fmt.Errorf("listing issues: %w", err)
	}
	var coverURL string
	for i := range issues {
		if strings.TrimSpace(issues[i].CoverURL) != "" {
			coverURL = issues[i].CoverURL
			break
		}
	}
	if coverURL == "" {
		slog.Info("series folder created without poster — no cover URL on any issue yet",
			"series_id", seriesID, "title", series.Title, "folder", folderPath)
		return nil
	}

	if err := s.downloadTo(ctx, coverURL, posterPath); err != nil {
		return fmt.Errorf("downloading cover from %q: %w", coverURL, err)
	}

	// Plex / Kodi look for cover.jpg, Mylar writes folder.jpg — write both.
	if data, err := os.ReadFile(posterPath); err == nil {
		_ = os.WriteFile(coverAlias, data, 0o644)
	}

	slog.Info("series folder + poster created",
		"series_id", seriesID, "title", series.Title, "folder", folderPath)
	return nil
}

// downloadTo streams a URL to a temp file and atomically renames to dest.
// Renaming is atomic on Windows when the dest doesn't already exist; we
// only call this when posterPath is missing, so the rename can't collide.
func (s *SeriesFolderService) downloadTo(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("upstream HTTP %d", resp.StatusCode)
	}
	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

// FolderBackfillResult summarizes a bulk folder-backfill pass.
type FolderBackfillResult struct {
	Total          int `json:"total"`
	Created        int `json:"created"`         // folder + poster created fresh
	AlreadyExisted int `json:"already_existed"` // folder.jpg already on disk
	NoCover        int `json:"no_cover"`        // folder created, no cover URL available
	Errors         int `json:"errors"`
}

// BackfillAllTracked runs EnsureFolderAndPoster for every tracked series.
// Sequential — each iteration does at most one CV/Metron image download,
// so a 200-series catalog finishes in a few minutes. Idempotent per series.
func (s *SeriesFolderService) BackfillAllTracked(ctx context.Context, progress func(processed, total int, msg string)) (*FolderBackfillResult, error) {
	tracked, err := s.seriesRepo.ListTracked()
	if err != nil {
		return nil, fmt.Errorf("listing tracked series: %w", err)
	}

	result := &FolderBackfillResult{Total: len(tracked)}
	for i, ser := range tracked {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		if progress != nil {
			progress(i, len(tracked), ser.Title)
		}

		folderName := buildSeriesFolderName(ser.Title, ser.Year)
		if folderName == "" {
			result.Errors++
			continue
		}
		folderPath := filepath.Join(s.librarySvc.GetLibraryDir(), folderName)
		posterPath := filepath.Join(folderPath, "folder.jpg")
		preExisted := false
		if _, err := os.Stat(posterPath); err == nil {
			preExisted = true
		}

		if err := s.EnsureFolderAndPoster(ctx, ser.ID); err != nil {
			slog.Warn("folder backfill: ensure", "series_id", ser.ID, "error", err)
			result.Errors++
			continue
		}

		switch {
		case preExisted:
			result.AlreadyExisted++
		case fileExists(posterPath):
			result.Created++
		default:
			// folder made but no cover URL was available
			result.NoCover++
		}
	}

	if progress != nil {
		progress(len(tracked), len(tracked), "folder backfill complete")
	}
	slog.Info("series folder backfill complete",
		"total", result.Total,
		"created", result.Created,
		"already_existed", result.AlreadyExisted,
		"no_cover", result.NoCover,
		"errors", result.Errors,
	)
	return result, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func buildSeriesFolderName(title string, year *int) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return ""
	}
	if year != nil {
		title = fmt.Sprintf("%s (%d)", title, *year)
	}
	return template.SanitizePathComponent(title)
}

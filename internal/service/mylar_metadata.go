package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/jeremy/longbox/internal/comicvine"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
)

// MylarMetadataService writes cvinfo and poster.jpg files to series folders.
type MylarMetadataService struct {
	seriesRepo *repository.SeriesRepo
	fileRepo   *repository.FileRepo
	cvClient   *comicvine.Client
	httpClient *http.Client
}

// NewMylarMetadataService creates a new service for writing Mylar3-compatible metadata files.
func NewMylarMetadataService(
	seriesRepo *repository.SeriesRepo,
	fileRepo *repository.FileRepo,
	cvClient *comicvine.Client,
) *MylarMetadataService {
	return &MylarMetadataService{
		seriesRepo: seriesRepo,
		fileRepo:   fileRepo,
		cvClient:   cvClient,
		httpClient: &http.Client{Timeout: 30 * 1000000000}, // 30 seconds
	}
}

// WriteAll writes cvinfo and poster.jpg for all series matched to ComicVine that have files on disk.
func (s *MylarMetadataService) WriteAll(
	ctx context.Context,
	progress func(processed, total int, message string),
) (written, skipped, failed int, err error) {
	seriesList, err := s.seriesRepo.ListWithComicVineID()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("listing series: %w", err)
	}

	total := len(seriesList)
	for i, series := range seriesList {
		select {
		case <-ctx.Done():
			return written, skipped, failed, ctx.Err()
		default:
		}

		progress(i, total, fmt.Sprintf("Writing metadata for %s (%d/%d)", series.Title, i+1, total))

		if series.ComicVineID == nil {
			skipped++
			continue
		}

		// Get files for this series to determine the folder
		files, err := s.fileRepo.ListBySeries(series.ID)
		if err != nil {
			slog.Warn("failed to list files for series", "series_id", series.ID, "title", series.Title, "error", err)
			failed++
			continue
		}
		if len(files) == 0 {
			slog.Debug("no files for series, skipping", "series_id", series.ID, "title", series.Title)
			skipped++
			continue
		}

		// Determine the series folder
		seriesDir := determineSeriesFolder(files)
		if seriesDir == "" {
			slog.Warn("could not determine series folder", "series_id", series.ID, "title", series.Title)
			skipped++
			continue
		}

		// Fetch volume info from ComicVine
		volume, err := s.cvClient.GetVolume(int(*series.ComicVineID))
		if err != nil {
			slog.Warn("failed to fetch volume from ComicVine",
				"series_id", series.ID,
				"title", series.Title,
				"cv_id", *series.ComicVineID,
				"error", err,
			)
			failed++
			continue
		}

		// Write cvinfo
		siteURL := volume.SiteURL
		if siteURL == "" {
			siteURL = fmt.Sprintf("https://comicvine.gamespot.com/volume/4050-%d/", *series.ComicVineID)
		}
		cvInfoPath := filepath.Join(seriesDir, "cvinfo")
		if err := os.WriteFile(cvInfoPath, []byte(siteURL+"\n"), 0644); err != nil {
			slog.Warn("failed to write cvinfo", "path", cvInfoPath, "error", err)
			failed++
			continue
		}

		// Download and write poster.jpg
		imageURL := bestImageURL(volume.Image)
		if imageURL != "" {
			posterPath := filepath.Join(seriesDir, "poster.jpg")
			if err := s.downloadImage(ctx, imageURL, posterPath); err != nil {
				slog.Warn("failed to download poster image",
					"series", series.Title,
					"url", imageURL,
					"error", err,
				)
				// cvinfo was written successfully, so still count as written
			}
		}

		written++
		slog.Info("wrote mylar metadata",
			"series", series.Title,
			"folder", seriesDir,
		)
	}

	progress(total, total, fmt.Sprintf("Done: %d written, %d skipped, %d failed", written, skipped, failed))
	return written, skipped, failed, nil
}

// determineSeriesFolder returns the most common parent directory among the given files.
func determineSeriesFolder(files []model.ComicFile) string {
	dirs := make(map[string]int)
	for _, f := range files {
		dir := filepath.Dir(f.FilePath)
		dirs[dir]++
	}

	var bestDir string
	var bestCount int
	for d, c := range dirs {
		if c > bestCount {
			bestDir = d
			bestCount = c
		}
	}
	return bestDir
}

// bestImageURL returns the best available image URL from a ComicVine Image object.
func bestImageURL(img *comicvine.Image) string {
	if img == nil {
		return ""
	}
	// Prefer super > original > screen_large > medium
	if img.SuperURL != "" {
		return img.SuperURL
	}
	if img.OriginalURL != "" {
		return img.OriginalURL
	}
	if img.ScreenLargeURL != "" {
		return img.ScreenLargeURL
	}
	if img.MediumURL != "" {
		return img.MediumURL
	}
	return ""
}

// downloadImage downloads an image from the given URL and writes it to destPath atomically.
func (s *MylarMetadataService) downloadImage(ctx context.Context, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "LongBox/1.0 (Comic Library Manager)")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("downloading image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("image download returned status %d", resp.StatusCode)
	}

	// Write to temp file first, then rename for atomicity
	dir := filepath.Dir(destPath)
	tmpFile, err := os.CreateTemp(dir, "poster-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	_, err = io.Copy(tmpFile, resp.Body)
	tmpFile.Close()
	if err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("writing image data: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

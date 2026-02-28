package service

import (
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/disintegration/imaging"
	"github.com/jeremy/longbox/internal/archive"
	"github.com/jeremy/longbox/internal/repository"
)

const thumbnailWidth = 300

type CoverService struct {
	coversDir string
	fileRepo  *repository.FileRepo
}

func NewCoverService(coversDir string, fileRepo *repository.FileRepo) *CoverService {
	return &CoverService{coversDir: coversDir, fileRepo: fileRepo}
}

// ExtractCover extracts the cover image from a comic file and saves a thumbnail.
// Returns the path to the saved thumbnail.
func (s *CoverService) ExtractCover(fileID int64, archivePath string) (string, error) {
	a, err := archive.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("opening archive: %w", err)
	}
	defer a.Close()

	entries, err := a.ListEntries()
	if err != nil {
		return "", fmt.Errorf("listing entries: %w", err)
	}

	cover := archive.FindCoverEntry(entries)
	if cover == nil {
		return "", fmt.Errorf("no cover image found in %s", archivePath)
	}

	rc, err := a.ExtractFile(cover.Name)
	if err != nil {
		return "", fmt.Errorf("extracting cover: %w", err)
	}
	defer rc.Close()

	thumbPath, err := s.createThumbnail(fileID, rc)
	if err != nil {
		return "", fmt.Errorf("creating thumbnail: %w", err)
	}

	if err := s.fileRepo.UpdateCoverPath(fileID, thumbPath); err != nil {
		slog.Warn("failed to update cover path in db", "file_id", fileID, "error", err)
	}

	return thumbPath, nil
}

func (s *CoverService) createThumbnail(fileID int64, r io.Reader) (string, error) {
	img, _, err := image.Decode(r)
	if err != nil {
		return "", fmt.Errorf("decoding image: %w", err)
	}

	thumb := imaging.Resize(img, thumbnailWidth, 0, imaging.Lanczos)

	filename := strconv.FormatInt(fileID, 10) + ".jpg"
	thumbPath := filepath.Join(s.coversDir, filename)

	out, err := os.Create(thumbPath)
	if err != nil {
		return "", fmt.Errorf("creating thumbnail file: %w", err)
	}
	defer out.Close()

	if err := jpeg.Encode(out, thumb, &jpeg.Options{Quality: 85}); err != nil {
		return "", fmt.Errorf("encoding thumbnail: %w", err)
	}

	return thumbPath, nil
}

// CoverPath returns the filesystem path to a cover thumbnail for a file ID.
func (s *CoverService) CoverPath(fileID int64) string {
	return filepath.Join(s.coversDir, strconv.FormatInt(fileID, 10)+".jpg")
}

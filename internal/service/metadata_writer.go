package service

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jeremy/longbox/internal/archive"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/util/trash"
)

// MetadataWriterService writes ComicInfo.xml metadata into comic archive files.
type MetadataWriterService struct {
	fileRepo   *repository.FileRepo
	issueRepo  *repository.IssueRepo
	seriesRepo *repository.SeriesRepo
}

// NewMetadataWriterService creates a new service for writing metadata to comic files.
func NewMetadataWriterService(
	fileRepo *repository.FileRepo,
	issueRepo *repository.IssueRepo,
	seriesRepo *repository.SeriesRepo,
) *MetadataWriterService {
	return &MetadataWriterService{
		fileRepo:   fileRepo,
		issueRepo:  issueRepo,
		seriesRepo: seriesRepo,
	}
}

// WriteResult reports the outcome of writing metadata to a single file.
type WriteResult struct {
	FileID   int64  `json:"file_id"`
	FileName string `json:"file_name"`
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Skipped  bool   `json:"skipped"`
}

// WriteMetadataToFile writes ComicInfo.xml into a single comic file.
// Only CBZ format is supported for writing. CBR/CB7 files will be skipped.
func (s *MetadataWriterService) WriteMetadataToFile(fileID int64) (*WriteResult, error) {
	file, err := s.fileRepo.GetByID(fileID)
	if err != nil {
		return nil, fmt.Errorf("fetching file: %w", err)
	}
	if file == nil {
		return nil, fmt.Errorf("file not found: %d", fileID)
	}

	return s.writeToFile(file)
}

// WriteMetadataForIssue writes ComicInfo.xml to the file linked to the given issue.
func (s *MetadataWriterService) WriteMetadataForIssue(issueID int64) (*WriteResult, error) {
	file, err := s.fileRepo.GetByIssueID(issueID)
	if err != nil {
		return nil, fmt.Errorf("fetching file for issue: %w", err)
	}
	if file == nil {
		return &WriteResult{
			Success: false,
			Message: "No file linked to this issue",
			Skipped: true,
		}, nil
	}

	return s.writeToFile(file)
}

// WriteMetadataForSeries writes ComicInfo.xml to all CBZ files in a series.
// Returns results for each file processed.
func (s *MetadataWriterService) WriteMetadataForSeries(seriesID int64) ([]WriteResult, error) {
	files, err := s.fileRepo.ListBySeries(seriesID)
	if err != nil {
		return nil, fmt.Errorf("listing files for series: %w", err)
	}

	results := make([]WriteResult, 0, len(files))
	for i := range files {
		result, err := s.writeToFile(&files[i])
		if err != nil {
			results = append(results, WriteResult{
				FileID:   files[i].ID,
				FileName: files[i].FileName,
				Success:  false,
				Message:  err.Error(),
			})
			continue
		}
		results = append(results, *result)
	}

	return results, nil
}

// writeToFile does the actual work of building ComicInfo from DB data and writing it.
// CBZ files are rewritten in place. CBR/CB7 files are converted to CBZ (extract +
// repack as ZIP, since RAR is read-only by license and 7z requires an external
// binary). The original archive is moved to the OS trash on successful conversion.
// PDFs are skipped — they have no ComicInfo concept.
func (s *MetadataWriterService) writeToFile(file *model.ComicFile) (*WriteResult, error) {
	result := &WriteResult{
		FileID:   file.ID,
		FileName: file.FileName,
	}

	switch file.FileFormat {
	case "cbz", "cbr", "cb7":
		// supported
	default:
		result.Skipped = true
		result.Message = fmt.Sprintf("Skipped: %s format is not supported for ComicInfo.xml", strings.ToUpper(file.FileFormat))
		return result, nil
	}

	if file.IssueID == nil {
		result.Skipped = true
		result.Message = "Skipped: file not linked to an issue"
		return result, nil
	}

	// Load issue
	issue, err := s.issueRepo.GetByID(*file.IssueID)
	if err != nil {
		return nil, fmt.Errorf("fetching issue %d: %w", *file.IssueID, err)
	}
	if issue == nil {
		result.Skipped = true
		result.Message = "Skipped: linked issue not found"
		return result, nil
	}

	// Load series
	series, err := s.seriesRepo.GetByID(issue.SeriesID)
	if err != nil {
		return nil, fmt.Errorf("fetching series %d: %w", issue.SeriesID, err)
	}
	if series == nil {
		result.Skipped = true
		result.Message = "Skipped: series not found"
		return result, nil
	}

	// Build ComicInfo
	ci := s.buildComicInfo(series, issue)

	// CBZ: rewrite in place. CBR/CB7: convert to CBZ alongside, then trash the
	// original. The DB row is updated with the new path/format/hash/size.
	if file.FileFormat == "cbz" {
		if err := archive.WriteComicInfoToCBZ(file.FilePath, ci); err != nil {
			result.Success = false
			result.Message = fmt.Sprintf("Failed: %s", err.Error())
			return result, nil
		}
		if err := s.fileRepo.UpdateHasComicInfo(file.ID, true); err != nil {
			slog.Warn("failed to update has_comicinfo flag", "file_id", file.ID, "error", err)
		}

		result.Success = true
		result.Message = "ComicInfo.xml written successfully"
		slog.Info("wrote ComicInfo.xml",
			"file", file.FileName,
			"series", series.Title,
			"issue", issue.IssueNumber,
		)
		return result, nil
	}

	converted, err := s.convertToCBZ(file, ci)
	if err != nil {
		result.Success = false
		result.Message = fmt.Sprintf("Conversion failed: %s", err.Error())
		return result, nil
	}

	result.Success = true
	result.FileName = converted.newName
	result.Message = fmt.Sprintf("Converted %s → CBZ and embedded ComicInfo.xml", strings.ToUpper(file.FileFormat))
	slog.Info("converted archive and wrote ComicInfo.xml",
		"old_path", file.FilePath,
		"new_path", converted.newPath,
		"series", series.Title,
		"issue", issue.IssueNumber,
	)
	return result, nil
}

// conversionResult tracks the new on-disk state after a CBR/CB7 → CBZ conversion.
type conversionResult struct {
	newPath string
	newName string
}

// convertToCBZ extracts a CBR/CB7 file, repacks it as a sibling .cbz with
// ComicInfo.xml embedded, updates the DB row, and trashes the original. Caller
// is responsible for serializing concurrent writes to the same file.
func (s *MetadataWriterService) convertToCBZ(file *model.ComicFile, ci *archive.ComicInfo) (*conversionResult, error) {
	dir := filepath.Dir(file.FilePath)
	baseNoExt := strings.TrimSuffix(filepath.Base(file.FilePath), filepath.Ext(file.FilePath))
	newName := baseNoExt + ".cbz"
	newPath := filepath.Join(dir, newName)

	if err := archive.ConvertToCBZ(file.FilePath, newPath, ci); err != nil {
		return nil, err
	}

	info, err := os.Stat(newPath)
	if err != nil {
		return nil, fmt.Errorf("stating new archive: %w", err)
	}
	hash, err := computeFileHash(newPath)
	if err != nil {
		slog.Warn("converted archive but failed to hash it", "path", newPath, "error", err)
	}

	if err := s.fileRepo.UpdateAfterConversion(file.ID, newPath, newName, "cbz", info.Size(), hash); err != nil {
		// Roll back the file write so we don't leave the DB and disk inconsistent.
		os.Remove(newPath)
		return nil, fmt.Errorf("updating DB after conversion: %w", err)
	}

	if err := trash.MoveToTrash(file.FilePath); err != nil {
		slog.Warn("converted archive but could not trash original", "path", file.FilePath, "error", err)
	}

	return &conversionResult{newPath: newPath, newName: newName}, nil
}

// buildComicInfo constructs a ComicInfo struct from the database models.
func (s *MetadataWriterService) buildComicInfo(series *model.Series, issue *model.Issue) *archive.ComicInfo {
	ci := &archive.ComicInfo{
		Series: series.Title,
		Number: issue.IssueNumber,
		Title:  issue.Title,
	}

	// Series metadata
	if series.Year != nil {
		ci.Year = *series.Year
	}
	if series.PublisherName != "" {
		ci.Publisher = series.PublisherName
	}
	if series.TotalIssues > 0 {
		ci.Count = series.TotalIssues
	}

	// Issue metadata
	if issue.Description != "" {
		ci.Summary = issue.Description
	}
	if issue.Writers != "" {
		ci.Writer = issue.Writers
	}
	if issue.Artists != "" {
		// Put combined artists string in Penciller field
		// since we don't have separate penciller/inker/colorist breakdown
		ci.Penciller = issue.Artists
	}

	// Parse cover_date (format: "YYYY-MM-DD" or "YYYY-MM" or "YYYY")
	if issue.CoverDate != "" {
		parts := strings.Split(issue.CoverDate, "-")
		if len(parts) >= 1 {
			if y, err := strconv.Atoi(parts[0]); err == nil {
				ci.Year = y
			}
		}
		if len(parts) >= 2 {
			if m, err := strconv.Atoi(parts[1]); err == nil {
				ci.Month = m
			}
		}
		if len(parts) >= 3 {
			if d, err := strconv.Atoi(parts[2]); err == nil {
				ci.Day = d
			}
		}
	}

	// ComicVine web link
	if issue.ComicVineID != nil {
		ci.Web = fmt.Sprintf("https://comicvine.gamespot.com/issue/4000-%d/", *issue.ComicVineID)
	} else if series.ComicVineID != nil {
		ci.Web = fmt.Sprintf("https://comicvine.gamespot.com/volume/4050-%d/", *series.ComicVineID)
	}

	return ci
}

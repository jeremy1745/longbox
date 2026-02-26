package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jeremy/longbox/internal/archive"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scanner"
)

type LibraryService struct {
	libraryDir string
	fileRepo   *repository.FileRepo
	seriesRepo *repository.SeriesRepo
	issueRepo  *repository.IssueRepo
	coverSvc   *CoverService
}

func NewLibraryService(
	libraryDir string,
	fileRepo *repository.FileRepo,
	seriesRepo *repository.SeriesRepo,
	issueRepo *repository.IssueRepo,
	coverSvc *CoverService,
) *LibraryService {
	return &LibraryService{
		libraryDir: libraryDir,
		fileRepo:   fileRepo,
		seriesRepo: seriesRepo,
		issueRepo:  issueRepo,
		coverSvc:   coverSvc,
	}
}

// SetLibraryDir updates the library directory used for scanning.
func (s *LibraryService) SetLibraryDir(dir string) {
	s.libraryDir = dir
}

// GetLibraryDir returns the current library directory.
func (s *LibraryService) GetLibraryDir() string {
	return s.libraryDir
}

// ScanResult holds the results of a library scan.
type ScanResult struct {
	FilesFound    int `json:"files_found"`
	FilesAdded    int `json:"files_added"`
	FilesSkipped  int `json:"files_skipped"`
	SeriesCreated int `json:"series_created"`
	IssuesCreated int `json:"issues_created"`
	Errors        int `json:"errors"`
}

// ScanProgressFunc reports scan progress. Can be nil.
type ScanProgressFunc func(processed, total int, message string)

// Scan performs a full library scan (blocking, no progress reporting).
func (s *LibraryService) Scan() (*ScanResult, error) {
	return s.ScanWithProgress(context.Background(), nil)
}

// ScanWithProgress performs a full library scan with progress reporting and cancellation.
func (s *LibraryService) ScanWithProgress(ctx context.Context, progress ScanProgressFunc) (*ScanResult, error) {
	results, err := scanner.Scan(s.libraryDir)
	if err != nil {
		return nil, err
	}

	result := &ScanResult{FilesFound: len(results)}
	total := len(results)

	for i, r := range results {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		if progress != nil {
			progress(i, total, fmt.Sprintf("Processing %s", r.Name))
		}

		exists, err := s.fileRepo.ExistsByPath(r.Path)
		if err != nil {
			slog.Error("error checking file existence", "path", r.Path, "error", err)
			result.Errors++
			continue
		}
		if exists {
			result.FilesSkipped++
			continue
		}

		added, err := s.processFile(r)
		if err != nil {
			slog.Error("error processing file", "path", r.Path, "error", err)
			result.Errors++
			continue
		}

		result.FilesAdded++
		if added.seriesCreated {
			result.SeriesCreated++
		}
		if added.issueCreated {
			result.IssuesCreated++
		}
	}

	if progress != nil {
		progress(total, total, "Scan complete")
	}

	slog.Info("library scan complete",
		"found", result.FilesFound,
		"added", result.FilesAdded,
		"skipped", result.FilesSkipped,
		"series_created", result.SeriesCreated,
		"issues_created", result.IssuesCreated,
		"errors", result.Errors,
	)

	return result, nil
}

// ProcessFiles processes specific files (used by the file watcher for incremental scans).
func (s *LibraryService) ProcessFiles(paths []string) (*ScanResult, error) {
	result := &ScanResult{FilesFound: len(paths)}
	for _, path := range paths {
		exists, err := s.fileRepo.ExistsByPath(path)
		if err != nil {
			slog.Error("error checking file existence", "path", path, "error", err)
			result.Errors++
			continue
		}
		if exists {
			result.FilesSkipped++
			continue
		}

		name := filepath.Base(path)
		size := int64(0)
		if info, err := os.Stat(path); err == nil {
			size = info.Size()
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))

		r := scanner.Result{Path: path, Name: name, Size: size, Format: ext}
		added, err := s.processFile(r)
		if err != nil {
			slog.Error("error processing file", "path", path, "error", err)
			result.Errors++
			continue
		}
		result.FilesAdded++
		if added.seriesCreated {
			result.SeriesCreated++
		}
		if added.issueCreated {
			result.IssuesCreated++
		}
	}

	if result.FilesAdded > 0 {
		slog.Info("incremental scan complete",
			"found", result.FilesFound,
			"added", result.FilesAdded,
			"skipped", result.FilesSkipped,
		)
	}

	return result, nil
}

type processResult struct {
	seriesCreated bool
	issueCreated  bool
}

func (s *LibraryService) processFile(r scanner.Result) (*processResult, error) {
	res := &processResult{}

	// Parse filename for metadata
	parsed := scanner.ParseFilename(r.Name)

	// Try to read ComicInfo.xml for richer metadata
	var comicInfo *archive.ComicInfo
	if r.Format == "cbz" || r.Format == "cbr" || r.Format == "cb7" {
		a, err := archive.Open(r.Path)
		if err == nil {
			comicInfo, _ = archive.ReadComicInfo(a)
			a.Close()
		}
	}

	// ComicInfo.xml takes priority over filename parsing
	seriesName := parsed.Series
	issueNumber := parsed.Number
	var year *int
	var writers, artists string

	if comicInfo != nil {
		if comicInfo.Series != "" {
			seriesName = comicInfo.Series
		}
		if comicInfo.Number != "" {
			issueNumber = comicInfo.Number
		}
		if comicInfo.Year > 0 {
			y := comicInfo.Year
			year = &y
		}
		writers = comicInfo.Writers()
		artists = comicInfo.Artists()
	}

	if year == nil && parsed.Year > 0 {
		year = &parsed.Year
	}

	// Find or create series
	series, err := s.findOrCreateSeries(seriesName, year)
	if err != nil {
		return nil, fmt.Errorf("finding/creating series: %w", err)
	}
	if series.CreatedAt.IsZero() == false && series.ID > 0 {
		res.seriesCreated = true
	}

	// Find or create issue
	var issueID *int64
	if issueNumber != "" {
		issue, created, err := s.findOrCreateIssue(series.ID, issueNumber, writers, artists)
		if err != nil {
			return nil, fmt.Errorf("finding/creating issue: %w", err)
		}
		issueID = &issue.ID
		if created {
			res.issueCreated = true
		}
	}

	// Create the comic file record
	cf := &model.ComicFile{
		FilePath:     r.Path,
		FileName:     r.Name,
		FileSize:     r.Size,
		FileFormat:   r.Format,
		IssueID:      issueID,
		ParsedSeries: parsed.Series,
		ParsedNumber: parsed.Number,
		HasComicInfo: comicInfo != nil,
	}
	if parsed.Year > 0 {
		cf.ParsedYear = &parsed.Year
	}
	if issueID != nil {
		cf.MatchConfidence = 1.0
	}

	if err := s.fileRepo.Create(cf); err != nil {
		return nil, fmt.Errorf("creating file record: %w", err)
	}

	// Extract cover thumbnail
	coverPath, err := s.coverSvc.ExtractCover(cf.ID, cf.FilePath)
	if err != nil {
		slog.Warn("failed to extract cover", "path", r.Path, "error", err)
	} else {
		cf.CoverPath = coverPath
	}

	// Set series cover to first file if not yet set
	if series.CoverFileID == nil {
		s.seriesRepo.UpdateCoverFileID(series.ID, cf.ID)
	}

	slog.Debug("processed comic file",
		"path", r.Path,
		"series", seriesName,
		"issue", issueNumber,
		"series_id", series.ID,
	)

	return res, nil
}

func (s *LibraryService) findOrCreateSeries(title string, year *int) (*model.Series, error) {
	existing, err := s.seriesRepo.FindByTitleAndYear(title, year)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	// Also try without year for fuzzy matching
	if year != nil {
		existing, err = s.seriesRepo.FindByTitleAndYear(title, nil)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			return existing, nil
		}
	}

	series := &model.Series{
		Title:     title,
		SortTitle: scanner.MakeSortTitle(title),
		Year:      intPtrToIntPtr(year),
		Status:    "unknown",
	}

	if err := s.seriesRepo.Create(series); err != nil {
		return nil, err
	}

	slog.Info("created new series", "title", title, "year", year, "id", series.ID)
	return series, nil
}

func (s *LibraryService) findOrCreateIssue(seriesID int64, number string, writers, artists string) (*model.Issue, bool, error) {
	existing, err := s.issueRepo.FindBySeriesAndNumber(seriesID, number)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}

	issue := &model.Issue{
		SeriesID:    seriesID,
		IssueNumber: number,
		SortNumber:  scanner.SortNumber(number),
		Writers:     writers,
		Artists:     artists,
		ReadStatus:  "unread",
	}

	if err := s.issueRepo.Create(issue); err != nil {
		return nil, false, err
	}

	return issue, true, nil
}

func intPtrToIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

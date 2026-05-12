package service

import (
	"context"
	"fmt"
	"hash/crc32"
	"io"
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

// ReattachResult summarizes a reattach-orphans pass.
type ReattachResult struct {
	Total                int `json:"total"`
	Attached             int `json:"attached"`
	SkippedNoSeriesParse int `json:"skipped_no_series_parse"`
	SkippedNoSeriesMatch int `json:"skipped_no_series_match"`
	SkippedNoIssueNumber int `json:"skipped_no_issue_number"`
	Errors               int `json:"errors"`
}

// pickClosestSeries chooses one series row from same-title candidates for
// the reattach pass. Rules:
//
//   - 0 candidates: return nil.
//   - 1 candidate: return it.
//   - N candidates and parsedYear > 0: pick the candidate whose year is
//     closest to parsedYear; ties broken by tracked, then CV-linked,
//     then lowest id.
//   - N candidates and parsedYear == 0: pick by tracked → CV-linked →
//     lowest id. (When we don't know which volume, "the tracked one"
//     is the user's de-facto choice.)
//
// Conservative on attribution risk: with no year hint and several
// volumes (e.g. nine "Transformers" volumes), the first tracked volume
// gets the file. If that's wrong, the file shows up under the wrong
// volume in the UI rather than as a silent orphan — easier to fix.
func pickClosestSeries(cands []model.Series, parsedYear int) *model.Series {
	if len(cands) == 0 {
		return nil
	}
	if len(cands) == 1 {
		return &cands[0]
	}
	best := -1
	bestScore := 0 // higher = better
	for i := range cands {
		score := 0
		if cands[i].Tracked {
			score += 10000
		}
		if cands[i].ComicVineID != nil {
			score += 1000
		}
		if parsedYear > 0 && cands[i].Year != nil {
			diff := *cands[i].Year - parsedYear
			if diff < 0 {
				diff = -diff
			}
			// Closer year → higher score. Max plausible diff is ~30y, so
			// a 100-point ceiling keeps year-proximity beneath tracked/CV.
			score += 100 - diff
		}
		// id tiebreaker: lower id wins (older = more likely canonical)
		score -= int(cands[i].ID) // small, just for stability when above ties
		if best < 0 || score > bestScore {
			best = i
			bestScore = score
		}
	}
	return &cands[best]
}

// ReattachOrphanFiles walks every comic_files row whose issue_id is NULL
// and tries to link it to an existing series + issue using the current
// parser (which is now stricter than the one that ran when most of these
// rows were first created). For each row:
//
//   1. Re-parse the filename. If empty, fall back to the parent-folder
//      name (filename "Daredevil 007.cbr" inside "E:\Comics\Daredevil
//      (2020)" still attaches correctly via the folder).
//   2. Look up an existing series by (title, year), or by title alone if
//      the year-strict lookup misses.
//   3. Find-or-create the matching issue.
//   4. Set comic_files.issue_id and refresh parsed_series / parsed_number /
//      parsed_year so they reflect the new parse.
//
// Never creates a series row — only reuses existing ones. Files that
// can't be matched stay as orphans and the caller sees the skip count.
func (s *LibraryService) ReattachOrphanFiles(ctx context.Context, progress ScanProgressFunc) (*ReattachResult, error) {
	orphans, err := s.fileRepo.ListOrphans()
	if err != nil {
		return nil, fmt.Errorf("listing orphan files: %w", err)
	}

	result := &ReattachResult{Total: len(orphans)}
	for i, cf := range orphans {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		if progress != nil {
			progress(i, len(orphans), cf.FileName)
		}

		// Parse the filename first; fall back to the parent folder when
		// the parser can't extract a series.
		parsed := scanner.ParseFilename(cf.FileName)
		seriesName := strings.TrimSpace(parsed.Series)
		issueNumber := parsed.Number
		year := parsed.Year
		if seriesName == "" {
			parent := filepath.Base(filepath.Dir(cf.FilePath))
			folderParsed := scanner.ParseFilename(parent + ".cbz") // synthetic ext so stripExtension is happy
			if folderParsed.Series != "" {
				seriesName = strings.TrimSpace(folderParsed.Series)
				if year == 0 {
					year = folderParsed.Year
				}
			}
		}

		if seriesName == "" {
			result.SkippedNoSeriesParse++
			continue
		}

		var yearPtr *int
		if year > 0 {
			y := year
			yearPtr = &y
		}
		series, err := s.seriesRepo.FindByTitleAndYear(seriesName, yearPtr)
		if err != nil {
			slog.Warn("reattach: lookup series", "file_id", cf.ID, "error", err)
			result.Errors++
			continue
		}
		if series == nil {
			// Strict (title, year) miss. Fall back to title-only and let
			// pickClosestSeries pick the right volume by year proximity.
			// pickClosestSeries returns nil if N==0 or the result is too
			// ambiguous to attribute safely.
			candidates, err := s.seriesRepo.ListByTitle(seriesName)
			if err != nil {
				slog.Warn("reattach: list by title", "file_id", cf.ID, "error", err)
				result.Errors++
				continue
			}
			series = pickClosestSeries(candidates, year)
		}
		if series == nil {
			result.SkippedNoSeriesMatch++
			continue
		}

		if strings.TrimSpace(issueNumber) == "" {
			result.SkippedNoIssueNumber++
			continue
		}

		issue, _, err := s.findOrCreateIssue(series.ID, issueNumber, "", "")
		if err != nil {
			slog.Warn("reattach: find-or-create issue", "file_id", cf.ID, "series_id", series.ID, "issue_number", issueNumber, "error", err)
			result.Errors++
			continue
		}

		if err := s.fileRepo.UpdateParsedAndIssue(cf.ID, issue.ID, parsed.Series, parsed.Number, yearPtr); err != nil {
			slog.Warn("reattach: update file", "file_id", cf.ID, "error", err)
			result.Errors++
			continue
		}
		result.Attached++
	}

	if progress != nil {
		progress(len(orphans), len(orphans), "Reattach complete")
	}
	slog.Info("reattach orphans complete",
		"total", result.Total,
		"attached", result.Attached,
		"skipped_no_parse", result.SkippedNoSeriesParse,
		"skipped_no_series", result.SkippedNoSeriesMatch,
		"skipped_no_issue", result.SkippedNoIssueNumber,
		"errors", result.Errors,
	)
	return result, nil
}

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

	// Try to read ComicInfo.xml for richer metadata.
	// Both archive.Open and archive.ReadComicInfo failures are logged at
	// debug — a missing/corrupt ComicInfo isn't fatal, but silently dropping
	// the error meant unusual archives fell back to filename-only parsing
	// with no signal to the user about which files needed attention.
	var comicInfo *archive.ComicInfo
	if r.Format == "cbz" || r.Format == "cbr" || r.Format == "cb7" {
		a, err := archive.Open(r.Path)
		if err != nil {
			slog.Debug("opening archive for ComicInfo read", "path", r.Path, "error", err)
		} else {
			ci, ciErr := archive.ReadComicInfo(a)
			if ciErr != nil {
				slog.Debug("reading ComicInfo.xml", "path", r.Path, "error", ciErr)
			} else {
				comicInfo = ci
			}
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

	// If neither the filename parser nor ComicInfo gave us a series name,
	// record the file as an orphan (issue_id=NULL) rather than minting a
	// Series row from the bare filename. Previously the parser fallback
	// would echo the whole filename back as the "series", which is how the
	// 14 garbage rows like "20th Century Men 01 (of 06) (2022) (Digital)"
	// ended up in the DB.
	if strings.TrimSpace(seriesName) == "" {
		slog.Debug("file has no parseable series — recording as orphan", "path", r.Path)
		cf := &model.ComicFile{
			FilePath:     r.Path,
			FileName:     r.Name,
			FileSize:     r.Size,
			FileFormat:   r.Format,
			ParsedSeries: parsed.Series,
			ParsedNumber: parsed.Number,
			HasComicInfo: comicInfo != nil,
		}
		if parsed.Year > 0 {
			cf.ParsedYear = &parsed.Year
		}
		if err := s.fileRepo.Create(cf); err != nil {
			return nil, fmt.Errorf("creating orphan file record: %w", err)
		}
		return res, nil
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

	// Compute file hash
	if hash, err := computeFileHash(r.Path); err != nil {
		slog.Debug("failed to compute file hash", "path", r.Path, "error", err)
	} else {
		cf.FileHash = hash
		s.fileRepo.UpdateHash(cf.ID, hash)
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
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("cannot create series with empty title")
	}
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

// computeFileHash computes a CRC32 hash of the file contents.
func computeFileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := crc32.NewIEEE()
	buf := make([]byte, 32*1024)
	if _, err := io.CopyBuffer(h, f, buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%08x", h.Sum32()), nil
}

// BackfillHashes computes CRC32 hashes for all files that don't have one.
func (s *LibraryService) BackfillHashes(ctx context.Context, progress func(processed, total int, message string)) (int, int, error) {
	files, err := s.fileRepo.ListUnhashed()
	if err != nil {
		return 0, 0, fmt.Errorf("listing unhashed files: %w", err)
	}

	total := len(files)
	hashed := 0
	failed := 0

	for i, f := range files {
		select {
		case <-ctx.Done():
			return hashed, failed, ctx.Err()
		default:
		}

		if progress != nil {
			progress(i, total, fmt.Sprintf("Hashing %s (%d/%d)", f.FileName, i+1, total))
		}

		hash, err := computeFileHash(f.FilePath)
		if err != nil {
			slog.Debug("failed to hash file", "path", f.FilePath, "error", err)
			failed++
			continue
		}

		if err := s.fileRepo.UpdateHash(f.ID, hash); err != nil {
			failed++
			continue
		}
		hashed++
	}

	if progress != nil {
		progress(total, total, fmt.Sprintf("Done: %d hashed, %d failed", hashed, failed))
	}
	return hashed, failed, nil
}

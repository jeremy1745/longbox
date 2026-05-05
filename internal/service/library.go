package service

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jeremy/longbox/internal/archive"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scanner"
	"github.com/jeremy/longbox/internal/util/trash"
)

type LibraryService struct {
	libraryDir   string
	fileRepo     *repository.FileRepo
	seriesRepo   *repository.SeriesRepo
	issueRepo    *repository.IssueRepo
	wantListRepo *repository.WantListRepo
	coverSvc     *CoverService

	// Reconciliation deps. Optional — if nil the relevant phase is skipped.
	settingRepo    *repository.SettingRepo
	metaSvc        *MetadataService
	backlogSvc     *BacklogService
	folderImageSvc *FolderImageService
}

// SetFolderImageService wires the poster refresher so every scan can end
// with a Phase E that materializes a series folder + cover.jpg for each
// series. Optional — when nil the phase is skipped.
func (s *LibraryService) SetFolderImageService(svc *FolderImageService) {
	s.folderImageSvc = svc
}

var ErrIssueNotFound = errors.New("issue not found")

func NewLibraryService(
	libraryDir string,
	fileRepo *repository.FileRepo,
	seriesRepo *repository.SeriesRepo,
	issueRepo *repository.IssueRepo,
	wantListRepo *repository.WantListRepo,
	coverSvc *CoverService,
) *LibraryService {
	return &LibraryService{
		libraryDir:   libraryDir,
		fileRepo:     fileRepo,
		seriesRepo:   seriesRepo,
		issueRepo:    issueRepo,
		wantListRepo: wantListRepo,
		coverSvc:     coverSvc,
	}
}

// SetReconcileDeps wires the dependencies needed for the CV-reconciliation
// phase of a scan. Both metaSvc and backlogSvc are optional; when nil that
// portion of the scan is skipped.
func (s *LibraryService) SetReconcileDeps(settingRepo *repository.SettingRepo, metaSvc *MetadataService, backlogSvc *BacklogService) {
	s.settingRepo = settingRepo
	s.metaSvc = metaSvc
	s.backlogSvc = backlogSvc
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
	FilesFound         int `json:"files_found"`
	FilesAdded         int `json:"files_added"`
	FilesSkipped       int `json:"files_skipped"`
	FilesRemoved       int `json:"files_removed"`
	SeriesCreated      int `json:"series_created"`
	IssuesCreated      int `json:"issues_created"`
	SeriesRefreshed    int `json:"series_refreshed"`
	IssuesNewlyMissing int `json:"issues_newly_missing"`
	BacklogRunsCreated int `json:"backlog_runs_created"`
	WantListPruned     int `json:"want_list_pruned"`
	SeriesMerged       int `json:"series_merged"`
	IssuesMerged       int `json:"issues_merged"`
	PostersWritten     int `json:"posters_written"`
	Errors             int `json:"errors"`
}

// ScanProgressFunc reports scan progress. Can be nil.
type ScanProgressFunc func(processed, total int, message string)

// ScanOptions tunes a single scan invocation.
type ScanOptions struct {
	// ForceCV ignores the per-series CV-refresh TTL during Phase C — every
	// tracked series with a ComicVine match is re-fetched.
	ForceCV bool
}

// Scan performs a full library scan (blocking, no progress reporting).
func (s *LibraryService) Scan() (*ScanResult, error) {
	return s.ScanWithProgress(context.Background(), nil)
}

// ScanWithProgress performs a full library scan with progress reporting and cancellation.
//
// The scan runs in three phases:
//   - Phase A (walk + add): newly-discovered files on disk are added to the DB.
//   - Phase B (reconcile disk → DB): rows whose path no longer exists on disk are removed.
//   - Phase C (reconcile DB → CV): if metaSvc is wired, tracked series past the
//     CV-refresh TTL are re-fetched from ComicVine; if scan_auto_queue_backlog is
//     enabled, a backlog run is created for each series with new gaps.
func (s *LibraryService) ScanWithProgress(ctx context.Context, progress ScanProgressFunc) (*ScanResult, error) {
	return s.ScanWithOptions(ctx, ScanOptions{}, progress)
}

// ScanWithOptions is the option-aware form of ScanWithProgress.
func (s *LibraryService) ScanWithOptions(ctx context.Context, opts ScanOptions, progress ScanProgressFunc) (*ScanResult, error) {
	results, err := scanner.Scan(s.libraryDir)
	if err != nil {
		return nil, err
	}

	result := &ScanResult{FilesFound: len(results)}
	total := len(results)

	// Phase A: walk + add new files.
	for i, r := range results {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		if progress != nil {
			progress(i, total, fmt.Sprintf("Indexing %s", r.Name))
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

	// Phase B: reconcile disk → DB. Drop rows whose file no longer exists.
	// Reuse the Phase A walk's path set so this is an O(N) hash lookup
	// instead of an os.Stat per row — critical on network shares where
	// each stat is a multi-ms RTT.
	if progress != nil {
		progress(total, total, "Reconciling library against disk")
	}
	present := make(map[string]struct{}, len(results))
	for _, r := range results {
		present[r.Path] = struct{}{}
	}
	if removed, err := s.reconcileDiskVsDB(ctx, present); err != nil {
		if errors.Is(err, context.Canceled) {
			return result, err
		}
		slog.Warn("disk reconciliation failed", "error", err)
		result.Errors++
	} else {
		result.FilesRemoved = removed
	}

	// Phase C: reconcile DB → CV (only if dependencies were wired).
	if s.metaSvc != nil && s.settingRepo != nil {
		if progress != nil {
			progress(total, total, "Reconciling against ComicVine")
		}
		if cv, err := s.reconcileDBVsCV(ctx, opts, progress); err != nil {
			if errors.Is(err, context.Canceled) {
				return result, err
			}
			slog.Warn("ComicVine reconciliation failed", "error", err)
			result.Errors++
		} else {
			result.SeriesRefreshed = cv.seriesRefreshed
			result.IssuesNewlyMissing = cv.newlyMissing
			result.BacklogRunsCreated = cv.backlogRunsCreated
		}
	}

	// Phase D: dedupe. Filename parsing routinely produces variants of the
	// same series/issue ("Ultimate Wolverine" vs "Ultimate.Wolverine.001"
	// vs "Ultimate Wolverine (2025)"); a CV/Metron match against a parsed
	// row often creates a fresh canonical series alongside the parsed one.
	// Run dedupe after every scan so duplicates never accumulate. Cheap
	// no-ops when nothing's duplicated.
	if progress != nil {
		progress(total, total, "Merging duplicate series and issues")
	}
	if dr, err := s.DedupeSeries(); err != nil {
		slog.Warn("post-scan series dedupe failed", "error", err)
	} else if dr.GroupsFound > 0 {
		slog.Info("post-scan series dedupe",
			"groups", dr.GroupsFound, "merged", dr.SeriesMerged, "files_relinked", dr.FilesRelinked)
		result.SeriesMerged = dr.SeriesMerged
	}
	if dr, err := s.DedupeIssues(); err != nil {
		slog.Warn("post-scan issue dedupe failed", "error", err)
	} else if dr.GroupsFound > 0 {
		slog.Info("post-scan issue dedupe",
			"groups", dr.GroupsFound, "deleted", dr.IssuesDeleted, "files_relinked", dr.FilesRelinked)
		result.IssuesMerged = int(dr.IssuesDeleted)
	}

	// Auto-prune any want list rows whose issue is now owned. Cheap one-shot
	// SQL — no per-row I/O. Must run AFTER Phase A and dedupe so newly-imported
	// files (and consolidated rows) fulfill their wants.
	if s.wantListRepo != nil {
		if pruned, err := s.wantListRepo.RemoveFulfilled(); err != nil {
			slog.Warn("want list prune failed", "error", err)
		} else if pruned > 0 {
			result.WantListPruned = pruned
			slog.Info("pruned fulfilled want list entries", "count", pruned)
		}
	}

	// Phase E: Mylar-parity poster refresh. Every series ends a scan
	// with a folder under the library + cover.jpg / folder.jpg at the
	// folder root, so the on-disk catalog stays "well organized" — every
	// series visible as its own shelf with a poster, even before any
	// issues are downloaded. force=false: only writes when content has
	// changed, so re-scans of a stable library are cheap. Errors are
	// logged but never fail the whole scan.
	if s.folderImageSvc != nil {
		if progress != nil {
			progress(total, total, "Refreshing series posters")
		}
		written, _, _, _, err := s.folderImageSvc.WriteAll(ctx, false, func(processed, count int, message string) {
			if progress != nil {
				progress(processed, count, message)
			}
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.Warn("post-scan poster refresh failed", "error", err)
		} else {
			result.PostersWritten = written
		}
	}

	if progress != nil {
		progress(total, total, scanSummaryMessage(result))
	}

	slog.Info("library scan complete",
		"found", result.FilesFound,
		"added", result.FilesAdded,
		"skipped", result.FilesSkipped,
		"removed", result.FilesRemoved,
		"series_created", result.SeriesCreated,
		"issues_created", result.IssuesCreated,
		"series_refreshed", result.SeriesRefreshed,
		"newly_missing", result.IssuesNewlyMissing,
		"backlog_runs", result.BacklogRunsCreated,
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

	// Parse filename for metadata.
	parsed := scanner.ParseFilename(r.Name)

	// Fall back to the parent folder name when the file name is too thin
	// to identify a series — Usenet releases routinely unpack as
	// `<release-folder>/<NN>.cbz` or `<release-folder>/<series> 001.cbz`
	// where the folder carries the series + year and the file alone
	// doesn't. Without this fallback those files get registered under
	// fragmentary series ("01", "Of") and the user has to manually rematch
	// every one. Apply only when the file's parsed series is missing or
	// suspiciously short (≤2 chars OR purely numeric); otherwise trust
	// the file name.
	weakSeries := len(strings.TrimSpace(parsed.Series)) <= 2 ||
		isNumericOrEmpty(parsed.Series)
	if weakSeries {
		parentName := filepath.Base(filepath.Dir(r.Path))
		if parentName != "" && parentName != "." && parentName != string(filepath.Separator) {
			parentParsed := scanner.ParseFilename(parentName)
			if strings.TrimSpace(parentParsed.Series) != "" &&
				len(parentParsed.Series) > len(strings.TrimSpace(parsed.Series)) {
				parsed.Series = parentParsed.Series
				if parsed.Number == "" {
					parsed.Number = parentParsed.Number
				}
				if parsed.Year == 0 && parentParsed.Year > 0 {
					parsed.Year = parentParsed.Year
				}
				slog.Debug("scan: fell back to parent folder for series",
					"file", r.Name, "parent", parentName,
					"series", parsed.Series, "issue", parsed.Number, "year", parsed.Year)
			}
		}
	}

	// Single-pass read for CBZ: hash the file, parse ComicInfo, and keep
	// the archive open so the cover-extract step doesn't re-read from
	// disk. On a network share each open is a multi-ms RTT, so two opens
	// per file × thousands of files is the dominant scan-time cost.
	//
	// Cap the in-memory shortcut at 200 MB; oversize files fall back to
	// the two-pass path (open+close for ComicInfo, separate hash, separate
	// archive open for cover extract). Annual collections, art books,
	// and trade paperbacks rarely exceed this.
	const maxInMemoryArchive int64 = 200 * 1024 * 1024
	var (
		comicInfo *archive.ComicInfo
		preHash   string
		openArch  archive.Archive
	)
	defer func() {
		if openArch != nil {
			openArch.Close()
		}
	}()
	if r.Format == "cbz" {
		bytes, hash, err := readFileWithHash(r.Path, maxInMemoryArchive)
		if err == nil && bytes != nil {
			if a, err := archive.OpenCBZBytes(bytes); err == nil {
				comicInfo, _ = archive.ReadComicInfo(a)
				openArch = a
				preHash = hash
			}
		}
	}
	if comicInfo == nil && (r.Format == "cbz" || r.Format == "cbr" || r.Format == "cb7") {
		// Fallback: oversize CBZ, CBR, or CB7 — open from path.
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
	series, created, err := s.findOrCreateSeries(seriesName, year)
	if err != nil {
		return nil, fmt.Errorf("finding/creating series: %w", err)
	}
	res.seriesCreated = created

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

	// Compute file hash. If the single-pass CBZ path already produced one,
	// reuse it — saves a second full file read.
	if preHash != "" {
		cf.FileHash = preHash
		s.fileRepo.UpdateHash(cf.ID, preHash)
	} else if hash, err := computeFileHash(r.Path); err != nil {
		slog.Debug("failed to compute file hash", "path", r.Path, "error", err)
	} else {
		cf.FileHash = hash
		s.fileRepo.UpdateHash(cf.ID, hash)
	}

	// Extract cover thumbnail. Reuse the open archive when we have one;
	// otherwise fall back to opening from path.
	var (
		coverPath string
		coverErr  error
	)
	if openArch != nil {
		coverPath, coverErr = s.coverSvc.ExtractCoverFromArchive(cf.ID, cf.FilePath, openArch)
	} else {
		coverPath, coverErr = s.coverSvc.ExtractCover(cf.ID, cf.FilePath)
	}
	if coverErr != nil {
		slog.Warn("failed to extract cover", "path", r.Path, "error", coverErr)
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

// findOrCreateSeries looks up a series by (title, year) and creates one when
// no match exists. Second return reports whether a new row was inserted —
// callers use this to count "series created" stats accurately. Returning
// `existing != nil` doesn't work because every row has a non-zero CreatedAt
// from the SQL default, so we have to track creation explicitly.
func (s *LibraryService) findOrCreateSeries(title string, year *int) (*model.Series, bool, error) {
	existing, err := s.seriesRepo.FindByTitleAndYear(title, year)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return existing, false, nil
	}

	// Also try without year for fuzzy matching
	if year != nil {
		existing, err = s.seriesRepo.FindByTitleAndYear(title, nil)
		if err != nil {
			return nil, false, err
		}
		if existing != nil {
			return existing, false, nil
		}
	}

	series := &model.Series{
		Title:     title,
		SortTitle: scanner.MakeSortTitle(title),
		Year:      intPtrToIntPtr(year),
		Status:    "unknown",
	}

	if err := s.seriesRepo.Create(series); err != nil {
		return nil, false, err
	}

	slog.Info("created new series", "title", title, "year", year, "id", series.ID)
	return series, true, nil
}

// DedupeResult summarizes a DedupeIssues run.
type DedupeResult struct {
	GroupsFound       int      `json:"groups_found"`
	IssuesDeleted     int64    `json:"issues_deleted"`
	FilesRelinked     int64    `json:"files_relinked"`
	WantsConsolidated int64    `json:"wants_consolidated"`
	ArcLinksCopied    int64    `json:"arc_links_copied"`
	Errors            []string `json:"errors,omitempty"`
}

// DedupeIssues finds (series_id, issue_number) groups with more than one
// issue row and merges every duplicate into a canonical issue (preferring the
// one with comicvine_id set). All comic_files / download_history / backlog_items
// references are reassigned to the canonical id, want_list / story_arc_issues
// memberships are copied via INSERT OR IGNORE, and the duplicate rows are
// deleted (CASCADE handles any leftover dependent rows).
func (s *LibraryService) DedupeIssues() (*DedupeResult, error) {
	groups, err := s.issueRepo.FindDuplicateIssueGroups()
	if err != nil {
		return nil, fmt.Errorf("finding duplicate issue groups: %w", err)
	}
	result := &DedupeResult{GroupsFound: len(groups)}
	for _, g := range groups {
		canonical, others, err := s.issueRepo.PickCanonicalIssueID(g.IssueIDs)
		if err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("series %d #%s: %v", g.SeriesID, g.IssueNumber, err))
			continue
		}
		filesRelinked, wantsCopied, arcLinksCopied, err := s.issueRepo.RelinkIssueRefs(canonical, others)
		if err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("series %d #%s relink: %v", g.SeriesID, g.IssueNumber, err))
			continue
		}
		deleted, err := s.issueRepo.DeleteByIDs(others)
		if err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("series %d #%s delete: %v", g.SeriesID, g.IssueNumber, err))
			continue
		}
		result.FilesRelinked += filesRelinked
		result.WantsConsolidated += wantsCopied
		result.ArcLinksCopied += arcLinksCopied
		result.IssuesDeleted += deleted
	}

	// Owned issues might have shed their wants in the consolidation; sweep up.
	if s.wantListRepo != nil {
		if pruned, err := s.wantListRepo.RemoveFulfilled(); err == nil && pruned > 0 {
			slog.Info("post-dedupe want list prune", "removed", pruned)
		}
	}

	slog.Info("dedupe issues complete",
		"groups_found", result.GroupsFound,
		"issues_deleted", result.IssuesDeleted,
		"files_relinked", result.FilesRelinked,
		"wants_consolidated", result.WantsConsolidated,
		"arc_links_copied", result.ArcLinksCopied,
		"errors", len(result.Errors),
	)
	return result, nil
}

// DedupeSeriesResult summarizes a DedupeSeries run.
type DedupeSeriesResult struct {
	GroupsFound       int      `json:"groups_found"`
	SeriesMerged      int      `json:"series_merged"`
	IssuesMoved       int      `json:"issues_moved"`
	IssuesConsolidated int     `json:"issues_consolidated"`
	FilesRelinked     int      `json:"files_relinked"`
	Errors            []string `json:"errors,omitempty"`
}

// DedupeSeries finds (normalized title, year) groups with more than one
// series row and merges every duplicate into a canonical series. Canonical
// preference: has comicvine_id, then metron_id, then most files, then lowest
// id. Reuses MergeSeriesInto for the per-pair work, which handles issues +
// files + want list + story arc memberships.
func (s *LibraryService) DedupeSeries() (*DedupeSeriesResult, error) {
	groups, err := s.seriesRepo.FindDuplicateSeriesGroups()
	if err != nil {
		return nil, fmt.Errorf("finding duplicate series groups: %w", err)
	}
	result := &DedupeSeriesResult{GroupsFound: len(groups)}
	for _, g := range groups {
		canonical, others, err := s.seriesRepo.PickCanonicalSeriesID(g.SeriesIDs)
		if err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("%q: %v", g.NormalizedTitle, err))
			continue
		}
		for _, srcID := range others {
			merge, err := s.MergeSeriesInto(srcID, canonical)
			if err != nil {
				result.Errors = append(result.Errors,
					fmt.Sprintf("%q (merge %d → %d): %v", g.NormalizedTitle, srcID, canonical, err))
				continue
			}
			result.SeriesMerged++
			result.IssuesMoved += merge.IssuesMoved
			result.IssuesConsolidated += merge.IssuesConsolidated
			result.FilesRelinked += merge.FilesRelinked
			if len(merge.Errors) > 0 {
				result.Errors = append(result.Errors, merge.Errors...)
			}
		}
	}

	// Wants pinned to merged-away series rows (now reassigned) might be
	// fulfilled — sweep up.
	if s.wantListRepo != nil {
		if pruned, err := s.wantListRepo.RemoveFulfilled(); err == nil && pruned > 0 {
			slog.Info("post-dedupe-series want list prune", "removed", pruned)
		}
	}

	slog.Info("dedupe series complete",
		"groups_found", result.GroupsFound,
		"series_merged", result.SeriesMerged,
		"issues_moved", result.IssuesMoved,
		"issues_consolidated", result.IssuesConsolidated,
		"files_relinked", result.FilesRelinked,
		"errors", len(result.Errors),
	)
	return result, nil
}

// PruneFulfilledWantList removes want_list rows whose issue now has a file.
// Returns the count removed. One-shot cleanup for legacy data.
func (s *LibraryService) PruneFulfilledWantList() (int, error) {
	if s.wantListRepo == nil {
		return 0, nil
	}
	return s.wantListRepo.RemoveFulfilled()
}

// DedupeFilesResult summarizes a DedupeFiles run.
type DedupeFilesResult struct {
	GroupsFound     int                    `json:"groups_found"`
	FilesTrashed    int                    `json:"files_trashed"`
	BytesReclaimed  int64                  `json:"bytes_reclaimed"`
	Errors          []string               `json:"errors,omitempty"`
	DryRun          bool                   `json:"dry_run"`
	Decisions       []DedupeFilesDecision  `json:"decisions"`
}

// DedupeFilesDecision reports the per-group choice DedupeFiles made — which
// file was kept, which were trashed, and why. Always returned (even when
// dry_run=true) so the user can review before running for real.
type DedupeFilesDecision struct {
	IssueID    int64    `json:"issue_id"`
	KeptID     int64    `json:"kept_id"`
	KeptPath   string   `json:"kept_path"`
	KeptReason string   `json:"kept_reason"`
	Trashed    []string `json:"trashed,omitempty"`
}

// DedupeFiles finds groups of comic_files attached to the same issue (often
// from filename variants like "Wonder Man 001.cbz" and "WonderMan-001.cbz")
// and trashes all but a canonical file via the OS recycle bin. Canonical
// preference: ComicInfo.xml present, then CBZ format, then largest size,
// then lowest id. dryRun=true performs the analysis but trashes nothing —
// use it for the "preview" UI flow.
func (s *LibraryService) DedupeFiles(dryRun bool) (*DedupeFilesResult, error) {
	groups, err := s.fileRepo.FindDuplicatesByIssue()
	if err != nil {
		return nil, fmt.Errorf("finding file duplicates: %w", err)
	}
	result := &DedupeFilesResult{GroupsFound: len(groups), DryRun: dryRun}
	for _, g := range groups {
		if len(g.Files) < 2 {
			continue
		}
		canonical, reason := pickCanonicalFile(g.Files)
		decision := DedupeFilesDecision{
			KeptID:     canonical.ID,
			KeptPath:   canonical.FilePath,
			KeptReason: reason,
		}
		if canonical.IssueID != nil {
			decision.IssueID = *canonical.IssueID
		}

		for _, f := range g.Files {
			if f.ID == canonical.ID {
				continue
			}
			decision.Trashed = append(decision.Trashed, f.FilePath)
			result.BytesReclaimed += f.FileSize
			if dryRun {
				continue
			}
			if err := trash.MoveToTrash(f.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				result.Errors = append(result.Errors,
					fmt.Sprintf("trash %q: %v", f.FilePath, err))
				continue
			}
			if f.CoverPath != "" {
				if err := os.Remove(f.CoverPath); err != nil && !os.IsNotExist(err) {
					slog.Warn("failed to remove duplicate cover thumb",
						"path", f.CoverPath, "error", err)
				}
			}
			// If a series cover pointed at the file we're trashing, clear
			// it so the next refresh adopts the canonical's thumbnail.
			if canonical.IssueID != nil {
				issue, err := s.issueRepo.GetByID(*canonical.IssueID)
				if err == nil && issue != nil {
					ser, err := s.seriesRepo.GetByID(issue.SeriesID)
					if err == nil && ser != nil && ser.CoverFileID != nil && *ser.CoverFileID == f.ID {
						_ = s.seriesRepo.ClearCoverFileID(ser.ID)
					}
				}
			}
			if err := s.fileRepo.Delete(f.ID); err != nil {
				result.Errors = append(result.Errors,
					fmt.Sprintf("delete row %d: %v", f.ID, err))
				continue
			}
			result.FilesTrashed++
		}
		result.Decisions = append(result.Decisions, decision)
	}

	slog.Info("dedupe files complete",
		"dry_run", dryRun,
		"groups", result.GroupsFound,
		"trashed", result.FilesTrashed,
		"bytes_reclaimed", result.BytesReclaimed,
		"errors", len(result.Errors),
	)
	return result, nil
}

// pickCanonicalFile chooses which duplicate to keep. Returns the chosen file
// and a human-readable reason for the dry-run preview.
func pickCanonicalFile(files []model.ComicFile) (model.ComicFile, string) {
	best := files[0]
	bestReason := "first"
	for i := 1; i < len(files); i++ {
		f := files[i]
		// 1. ComicInfo presence wins.
		if f.HasComicInfo && !best.HasComicInfo {
			best, bestReason = f, "has ComicInfo.xml"
			continue
		}
		if best.HasComicInfo && !f.HasComicInfo {
			continue
		}
		// 2. CBZ beats CBR / CB7.
		bestIsCBZ := strings.EqualFold(best.FileFormat, "cbz")
		fIsCBZ := strings.EqualFold(f.FileFormat, "cbz")
		if fIsCBZ && !bestIsCBZ {
			best, bestReason = f, "CBZ format"
			continue
		}
		if bestIsCBZ && !fIsCBZ {
			continue
		}
		// 3. Larger file wins (better quality typically).
		if f.FileSize > best.FileSize {
			best, bestReason = f, "largest size"
			continue
		}
		// 4. Lowest id (stable tiebreaker).
		if f.FileSize == best.FileSize && f.ID < best.ID {
			best, bestReason = f, "earliest record"
		}
	}
	return best, bestReason
}

// MergeResult summarizes what MergeSeriesInto did.
type MergeResult struct {
	IssuesMoved        int      `json:"issues_moved"`         // src issue rows reassigned to dst (no dst counterpart)
	IssuesConsolidated int      `json:"issues_consolidated"`  // src issue rows whose files moved onto an existing dst issue, then deleted
	FilesRelinked      int      `json:"files_relinked"`       // comic_files rows whose issue_id changed
	Errors             []string `json:"errors,omitempty"`
}

// MergeSeriesInto consolidates src into dst. Every issue in src is either:
//   - reassigned to dst (when dst has no issue with the same issue_number), or
//   - "consolidated" — its comic_files are re-linked onto dst's matching
//     issue and src's duplicate issue row is deleted.
// After all issues have been moved out, src's series row is deleted. Dst's
// series row is left untouched (its CV match, tracked flag, etc. all survive).
func (s *LibraryService) MergeSeriesInto(srcID, dstID int64) (*MergeResult, error) {
	if srcID == dstID {
		return nil, fmt.Errorf("cannot merge a series into itself")
	}
	src, err := s.seriesRepo.GetByID(srcID)
	if err != nil {
		return nil, fmt.Errorf("looking up src series: %w", err)
	}
	if src == nil {
		return nil, fmt.Errorf("src series %d not found", srcID)
	}
	dst, err := s.seriesRepo.GetByID(dstID)
	if err != nil {
		return nil, fmt.Errorf("looking up dst series: %w", err)
	}
	if dst == nil {
		return nil, fmt.Errorf("dst series %d not found", dstID)
	}

	srcIssues, err := s.issueRepo.ListBySeries(srcID)
	if err != nil {
		return nil, fmt.Errorf("listing src issues: %w", err)
	}

	result := &MergeResult{}
	for _, issue := range srcIssues {
		existing, err := s.issueRepo.FindBySeriesAndNumber(dstID, issue.IssueNumber)
		if err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("looking up dst issue #%s: %v", issue.IssueNumber, err))
			continue
		}

		if existing == nil {
			// dst has no counterpart — move the row over.
			if err := s.issueRepo.UpdateSeriesID(issue.ID, dstID); err != nil {
				result.Errors = append(result.Errors,
					fmt.Sprintf("reassigning issue #%s: %v", issue.IssueNumber, err))
				continue
			}
			result.IssuesMoved++
			continue
		}

		// dst already has the same issue_number. Move every comic_files row
		// off the src issue and onto the dst issue, then delete src's row.
		srcFiles, err := s.fileRepo.ListBySeries(srcID)
		if err == nil {
			for _, f := range srcFiles {
				if f.IssueID == nil || *f.IssueID != issue.ID {
					continue
				}
				if err := s.fileRepo.UpdateIssueID(f.ID, existing.ID); err != nil {
					result.Errors = append(result.Errors,
						fmt.Sprintf("relinking file %d for issue #%s: %v", f.ID, issue.IssueNumber, err))
					continue
				}
				result.FilesRelinked++
			}
		}

		if err := s.issueRepo.Delete(issue.ID); err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("deleting consolidated issue #%s: %v", issue.IssueNumber, err))
			continue
		}
		result.IssuesConsolidated++
	}

	// Delete the now-empty src series.
	if err := s.seriesRepo.Delete(srcID); err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("deleting src series: %v", err))
		return result, nil
	}

	slog.Info("merged series",
		"src_id", srcID,
		"src_title", src.Title,
		"dst_id", dstID,
		"dst_title", dst.Title,
		"issues_moved", result.IssuesMoved,
		"issues_consolidated", result.IssuesConsolidated,
		"files_relinked", result.FilesRelinked,
		"errors", len(result.Errors),
	)
	return result, nil
}

// DeleteSeries removes a series end-to-end: every file moved to OS trash,
// every issue + comic_files row deleted, any child (annual) series detached
// via parent_series_id := NULL, and finally the series row itself deleted.
// Returns the wrapped DeleteAllIssuesInSeries counts.
func (s *LibraryService) DeleteSeries(seriesID int64) (*BulkDeleteResult, error) {
	series, err := s.seriesRepo.GetByID(seriesID)
	if err != nil {
		return nil, fmt.Errorf("looking up series: %w", err)
	}
	if series == nil {
		return nil, fmt.Errorf("series %d not found", seriesID)
	}

	// Step 1: trash every issue's file + delete issue + file rows.
	result, err := s.DeleteAllIssuesInSeries(seriesID)
	if err != nil {
		return nil, err
	}

	// Step 2: detach any child series (annuals/specials linked to this one).
	children, err := s.seriesRepo.GetChildSeries(seriesID)
	if err == nil {
		for _, child := range children {
			if err := s.seriesRepo.SetParentSeries(child.ID, nil); err != nil {
				slog.Warn("failed to detach child series", "child_id", child.ID, "parent_id", seriesID, "error", err)
				if result != nil {
					result.Errors = append(result.Errors,
						fmt.Sprintf("detach child %d: %v", child.ID, err))
				}
			}
		}
	}

	// Step 3: delete the series row. backlog_runs CASCADE off this.
	if err := s.seriesRepo.Delete(seriesID); err != nil {
		return result, fmt.Errorf("deleting series row: %w", err)
	}

	slog.Info("deleted series",
		"series_id", seriesID,
		"title", series.Title,
		"issues_deleted", result.IssuesDeleted,
		"files_trashed", result.FilesTrashed,
		"detached_children", len(children),
	)
	return result, nil
}

// DeleteAllIssuesInSeries trashes the on-disk file and deletes both the
// comic_files row and the issue row for every issue in the series. The series
// row itself is preserved. Returns counts and any per-issue errors as a
// summary.
type BulkDeleteResult struct {
	IssuesDeleted int      `json:"issues_deleted"`
	FilesTrashed  int      `json:"files_trashed"`
	Errors        []string `json:"errors,omitempty"`
}

func (s *LibraryService) DeleteAllIssuesInSeries(seriesID int64) (*BulkDeleteResult, error) {
	series, err := s.seriesRepo.GetByID(seriesID)
	if err != nil {
		return nil, fmt.Errorf("looking up series: %w", err)
	}
	if series == nil {
		return nil, fmt.Errorf("series %d not found", seriesID)
	}

	issues, err := s.issueRepo.ListBySeries(seriesID)
	if err != nil {
		return nil, fmt.Errorf("listing issues: %w", err)
	}

	result := &BulkDeleteResult{}
	for _, issue := range issues {
		if err := s.DeleteIssue(issue.ID); err != nil {
			if errors.Is(err, ErrIssueNotFound) {
				continue
			}
			result.Errors = append(result.Errors,
				fmt.Sprintf("issue #%s (id=%d): %v", issue.IssueNumber, issue.ID, err))
			continue
		}
		result.IssuesDeleted++
		if issue.HasFile {
			result.FilesTrashed++
		}
	}

	if series.CoverFileID != nil {
		if err := s.seriesRepo.ClearCoverFileID(seriesID); err != nil {
			slog.Warn("failed to clear series cover after bulk delete", "series_id", seriesID, "error", err)
		}
	}

	slog.Info("bulk delete series issues complete",
		"series_id", seriesID,
		"title", series.Title,
		"deleted", result.IssuesDeleted,
		"trashed", result.FilesTrashed,
		"errors", len(result.Errors),
	)

	return result, nil
}

func (s *LibraryService) DeleteIssue(issueID int64) error {
	issue, err := s.issueRepo.GetByID(issueID)
	if err != nil {
		return err
	}
	if issue == nil {
		return ErrIssueNotFound
	}

	file, err := s.fileRepo.GetByIssueID(issueID)
	if err != nil {
		return err
	}
	if file != nil {
		if err := trash.MoveToTrash(file.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("moving file to trash: %w", err)
		}
		if file.CoverPath != "" {
			if err := os.Remove(file.CoverPath); err != nil && !os.IsNotExist(err) {
				slog.Warn("failed to remove cover thumbnail", "path", file.CoverPath, "error", err)
			}
		}
		if err := s.fileRepo.Delete(file.ID); err != nil {
			return err
		}
		series, err := s.seriesRepo.GetByID(issue.SeriesID)
		if err == nil && series != nil && series.CoverFileID != nil && *series.CoverFileID == file.ID {
			if err := s.seriesRepo.ClearCoverFileID(series.ID); err != nil {
				slog.Warn("failed to clear series cover", "series_id", series.ID, "error", err)
			}
		}
	}

	if err := s.issueRepo.Delete(issueID); err != nil {
		return err
	}

	return nil
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
// AdoptStrandedFoldersResult summarizes a stranded-folder pass.
type AdoptStrandedFoldersResult struct {
	FoldersScanned   int      `json:"folders_scanned"`
	FilesReassigned  int      `json:"files_reassigned"`
	SeriesCreated    int      `json:"series_created"`
	IssuesCreated    int      `json:"issues_created"`
	Errors           []string `json:"errors,omitempty"`
}

// AdoptStrandedFolders walks every immediate subfolder of the library
// directory looking for SAB-style download folders — folders whose name
// looks like a release ("Series Title NN (of MM) (YYYY) (Digital) (Group)")
// and that contain comic files. For each match, the folder name is parsed
// to recover series/issue/year; comic_files rows pointing at the inside
// files get reassigned to the correct (series, issue) — creating those DB
// rows if needed — so a subsequent Reorganize moves them to the canonical
// `Series (Year)/Series (Year) NNN.ext` layout and cleans up the now-empty
// release folder.
//
// Files only — folders are not deleted by this pass; the Reorganize
// `cleanEmptyDirs` step does that after the moves.
// TrashOrphanFilesResult summarizes a trash-orphans pass.
type TrashOrphanFilesResult struct {
	Scanned        int      `json:"scanned"`
	FilesTrashed   int      `json:"files_trashed"`
	BytesReclaimed int64    `json:"bytes_reclaimed"`
	DryRun         bool     `json:"dry_run"`
	Errors         []string `json:"errors,omitempty"`
	Trashed        []string `json:"trashed,omitempty"`
}

// TrashOrphanFiles trashes every comic_files row whose issue_id is NULL.
// These rows are files that LongBox can no longer link to any issue —
// usually casualties of historical dedupe-issues passes whose
// RelinkIssueRefs missed them, or filenames the parser couldn't match
// against existing series. They're invisible to reorganize and dedupe-
// files (both rely on issue_id), so they accumulate forever and keep
// non-canonical folders alive on disk.
//
// Both the on-disk file and the DB row are removed. Files go to the OS
// recycle bin (reversible from there) via the existing trash util.
// dryRun=true previews without touching disk or DB.
func (s *LibraryService) TrashOrphanFiles(ctx context.Context, dryRun bool) (*TrashOrphanFilesResult, error) {
	orphans, err := s.fileRepo.ListOrphanFiles()
	if err != nil {
		return nil, fmt.Errorf("listing orphan files: %w", err)
	}
	res := &TrashOrphanFilesResult{Scanned: len(orphans), DryRun: dryRun}
	for _, f := range orphans {
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		default:
		}
		res.Trashed = append(res.Trashed, f.FilePath)
		res.BytesReclaimed += f.FileSize
		if dryRun {
			res.FilesTrashed++
			continue
		}
		if err := trash.MoveToTrash(f.FilePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			res.Errors = append(res.Errors,
				fmt.Sprintf("trash %s: %v", f.FilePath, err))
			continue
		}
		if f.CoverPath != "" {
			if rmErr := os.Remove(f.CoverPath); rmErr != nil && !os.IsNotExist(rmErr) {
				slog.Debug("could not remove orphan cover thumb",
					"path", f.CoverPath, "error", rmErr)
			}
		}
		if err := s.fileRepo.Delete(f.ID); err != nil {
			res.Errors = append(res.Errors,
				fmt.Sprintf("delete row %d: %v", f.ID, err))
			continue
		}
		res.FilesTrashed++
	}
	slog.Info("trash-orphans complete",
		"dry_run", dryRun, "scanned", res.Scanned,
		"trashed", res.FilesTrashed, "errors", len(res.Errors))
	return res, nil
}

// ReattachOrphanFilesResult summarizes a reattach pass.
type ReattachOrphanFilesResult struct {
	Scanned        int      `json:"scanned"`
	Reattached     int      `json:"reattached"`
	IssuesCreated  int      `json:"issues_created"`
	SeriesCreated  int      `json:"series_created"`
	StillOrphaned  int      `json:"still_orphaned"`
	Errors         []string `json:"errors,omitempty"`
}

// ReattachOrphanFiles walks every comic_files row whose issue_id is NULL
// and tries to relink it to a series + issue based on the path. Orphans
// arise when dedupe-issues deletes an issue row whose RelinkIssueRefs
// missed something (or from older imports that landed without a link).
// Reorganize ignores rows with no issue_id, so these files become
// invisible to the normal cleanup. This pass parses the file's parent
// folder + filename for series/issue metadata, find-or-creates the
// series + issue, and sets issue_id. After running, reorganize will see
// the rows and move them into canonical layout.
func (s *LibraryService) ReattachOrphanFiles(ctx context.Context, progress func(processed, total int, message string)) (*ReattachOrphanFilesResult, error) {
	if progress == nil {
		progress = func(int, int, string) {}
	}
	orphans, err := s.fileRepo.ListOrphanFiles()
	if err != nil {
		return nil, fmt.Errorf("listing orphan files: %w", err)
	}
	res := &ReattachOrphanFilesResult{Scanned: len(orphans)}
	for i, f := range orphans {
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		default:
		}
		progress(i, len(orphans), fmt.Sprintf("Reattaching %s (%d/%d)",
			filepath.Base(f.FilePath), i+1, len(orphans)))

		// Parse filename + parent folder for series/issue/year
		parsed := scanner.ParseFilename(f.FileName)
		seriesName := parsed.Series
		issueNumber := parsed.Number
		var year *int
		if parsed.Year > 0 {
			y := parsed.Year
			year = &y
		}

		weak := len(strings.TrimSpace(seriesName)) <= 2 || isNumericOrEmpty(seriesName)
		if weak {
			parentName := filepath.Base(filepath.Dir(f.FilePath))
			if parentName != "" && parentName != "." {
				parentParsed := scanner.ParseFilename(parentName)
				if strings.TrimSpace(parentParsed.Series) != "" {
					seriesName = parentParsed.Series
					if issueNumber == "" {
						issueNumber = parentParsed.Number
					}
					if year == nil && parentParsed.Year > 0 {
						y := parentParsed.Year
						year = &y
					}
				}
			}
		}

		// Fall back to the stored parsed_series/parsed_number from import.
		if seriesName == "" && f.ParsedSeries != "" {
			seriesName = f.ParsedSeries
		}
		if issueNumber == "" && f.ParsedNumber != "" {
			issueNumber = f.ParsedNumber
		}
		if year == nil && f.ParsedYear != nil {
			year = f.ParsedYear
		}

		if seriesName == "" || issueNumber == "" {
			res.StillOrphaned++
			continue
		}

		series, seriesCreated, err := s.findOrCreateSeries(seriesName, year)
		if err != nil {
			res.Errors = append(res.Errors,
				fmt.Sprintf("series for %s: %v", f.FileName, err))
			res.StillOrphaned++
			continue
		}
		if seriesCreated {
			res.SeriesCreated++
		}

		issue, issueCreated, err := s.findOrCreateIssue(series.ID, issueNumber, "", "")
		if err != nil {
			res.Errors = append(res.Errors,
				fmt.Sprintf("issue %s #%s: %v", series.Title, issueNumber, err))
			res.StillOrphaned++
			continue
		}
		if issueCreated {
			res.IssuesCreated++
		}

		if err := s.fileRepo.UpdateIssueID(f.ID, issue.ID); err != nil {
			res.Errors = append(res.Errors,
				fmt.Sprintf("update %s issue_id: %v", f.FileName, err))
			res.StillOrphaned++
			continue
		}
		res.Reattached++
	}
	progress(len(orphans), len(orphans), fmt.Sprintf(
		"Reattached %d / %d orphan files (%d still orphaned, %d series + %d issues created)",
		res.Reattached, res.Scanned, res.StillOrphaned, res.SeriesCreated, res.IssuesCreated))
	return res, nil
}

func (s *LibraryService) AdoptStrandedFolders(ctx context.Context, progress func(processed, total int, message string)) (*AdoptStrandedFoldersResult, error) {
	if progress == nil {
		progress = func(int, int, string) {}
	}
	libraryDir := s.libraryDir
	if libraryDir == "" {
		return nil, fmt.Errorf("library directory not configured")
	}

	entries, err := os.ReadDir(libraryDir)
	if err != nil {
		return nil, fmt.Errorf("reading library dir: %w", err)
	}

	result := &AdoptStrandedFoldersResult{}
	progress(0, len(entries), "Scanning top-level folders for stranded releases")

	for i, e := range entries {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		if !e.IsDir() {
			continue
		}
		dirPath := filepath.Join(libraryDir, e.Name())
		progress(i, len(entries), fmt.Sprintf("Inspecting %s", e.Name()))

		// Parse the folder name. If it doesn't look like a release (no
		// detectable issue number AND no year), skip — could be a real
		// canonical series folder that already has its own structure.
		parentParsed := scanner.ParseFilename(e.Name())
		hasIssue := strings.TrimSpace(parentParsed.Number) != ""
		hasYear := parentParsed.Year > 0
		hasSeries := strings.TrimSpace(parentParsed.Series) != ""
		if !hasSeries || (!hasIssue && !hasYear) {
			continue
		}

		// Collect comic files inside this folder (one level deep — most
		// SAB extractions are flat).
		childEntries, err := os.ReadDir(dirPath)
		if err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("read %s: %v", dirPath, err))
			continue
		}
		var childFiles []string
		for _, c := range childEntries {
			if c.IsDir() {
				continue
			}
			lower := strings.ToLower(c.Name())
			if strings.HasSuffix(lower, ".cbz") || strings.HasSuffix(lower, ".cbr") || strings.HasSuffix(lower, ".cb7") {
				childFiles = append(childFiles, filepath.Join(dirPath, c.Name()))
			}
		}
		if len(childFiles) == 0 {
			continue
		}
		result.FoldersScanned++

		// Resolve series via the same path the scan uses — find-or-create
		// by sort_title + year.
		var year *int
		if parentParsed.Year > 0 {
			y := parentParsed.Year
			year = &y
		}
		series, seriesCreated, err := s.findOrCreateSeries(parentParsed.Series, year)
		if err != nil {
			result.Errors = append(result.Errors,
				fmt.Sprintf("series for %s: %v", e.Name(), err))
			continue
		}
		if seriesCreated {
			result.SeriesCreated++
		}

		// One file in the folder = the issue from the folder name.
		// Multiple files = each its own issue (rare, but a TPB collection
		// or multi-issue release can land this way) — fall back to per-
		// file parsing for the issue number.
		for _, fp := range childFiles {
			issueNum := parentParsed.Number
			if len(childFiles) > 1 {
				fp := scanner.ParseFilename(filepath.Base(fp))
				if strings.TrimSpace(fp.Number) != "" {
					issueNum = fp.Number
				}
			}
			if issueNum == "" {
				result.Errors = append(result.Errors,
					fmt.Sprintf("%s: no issue number resolvable", filepath.Base(fp)))
				continue
			}

			issue, issueCreated, err := s.findOrCreateIssue(series.ID, issueNum, "", "")
			if err != nil {
				result.Errors = append(result.Errors,
					fmt.Sprintf("issue %s #%s: %v", series.Title, issueNum, err))
				continue
			}
			if issueCreated {
				result.IssuesCreated++
			}

			// Reassign the comic_files row (or create one if scan never
			// touched this path).
			cf, err := s.fileRepo.GetByPath(fp)
			if err != nil {
				result.Errors = append(result.Errors,
					fmt.Sprintf("file %s lookup: %v", fp, err))
				continue
			}
			if cf == nil {
				// Not in DB yet — let processFile (called via ProcessFiles)
				// register it; with the parent-folder fallback now in place
				// the result will already point at the right series/issue.
				if _, err := s.ProcessFiles([]string{fp}); err != nil {
					result.Errors = append(result.Errors,
						fmt.Sprintf("file %s import: %v", fp, err))
					continue
				}
				cf, err = s.fileRepo.GetByPath(fp)
				if err != nil || cf == nil {
					continue
				}
			}
			if cf.IssueID == nil || *cf.IssueID != issue.ID {
				if err := s.fileRepo.UpdateIssueID(cf.ID, issue.ID); err != nil {
					result.Errors = append(result.Errors,
						fmt.Sprintf("file %s relink: %v", fp, err))
					continue
				}
				result.FilesReassigned++
			}
		}
	}

	progress(len(entries), len(entries), fmt.Sprintf(
		"Adopted %d folders · %d files reassigned · %d series + %d issues created",
		result.FoldersScanned, result.FilesReassigned, result.SeriesCreated, result.IssuesCreated))
	return result, nil
}

// isNumericOrEmpty reports whether the string is empty after trimming or
// consists only of digits — used to detect "weak" parsed series names like
// "01" / "" that should defer to a richer source (parent folder name,
// ComicInfo.xml).
func isNumericOrEmpty(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func computeFileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return hashReader(f)
}

func hashReader(r io.Reader) (string, error) {
	h := crc32.NewIEEE()
	buf := make([]byte, 64*1024)
	if _, err := io.CopyBuffer(h, r, buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%08x", h.Sum32()), nil
}

// readFileWithHash reads the entire file into memory and returns the bytes
// plus the CRC32 hash. Single read pass — used by processFile to avoid
// opening the file twice (once for archive parsing, once for hashing) on
// network-attached libraries where each round-trip is expensive.
//
// Memory: a single CBZ is typically ≤200 MB; the buffer is short-lived.
// Aborts (returning the empty hash) when the file is larger than maxBytes
// so a corrupt or pathologically-large entry can't OOM the scan.
func readFileWithHash(path string, maxBytes int64) ([]byte, string, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, "", err
	}
	if maxBytes > 0 && stat.Size() > maxBytes {
		// File too big for the in-memory shortcut; caller can fall back to
		// the two-pass path. Return empty hash so the caller knows.
		return nil, "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	buf := make([]byte, 0, stat.Size())
	h := crc32.NewIEEE()
	tmp := make([]byte, 64*1024)
	for {
		n, rerr := f.Read(tmp)
		if n > 0 {
			h.Write(tmp[:n])
			buf = append(buf, tmp[:n]...)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, "", rerr
		}
	}
	return buf, fmt.Sprintf("%08x", h.Sum32()), nil
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

// scanSummaryMessage builds a one-line summary of a completed scan suitable
// for the active-job banner.
func scanSummaryMessage(r *ScanResult) string {
	parts := []string{
		fmt.Sprintf("found %d", r.FilesFound),
		fmt.Sprintf("added %d", r.FilesAdded),
	}
	if r.FilesRemoved > 0 {
		parts = append(parts, fmt.Sprintf("removed %d", r.FilesRemoved))
	}
	if r.SeriesRefreshed > 0 {
		parts = append(parts, fmt.Sprintf("CV refreshed %d", r.SeriesRefreshed))
	}
	if r.IssuesNewlyMissing > 0 {
		parts = append(parts, fmt.Sprintf("%d newly missing", r.IssuesNewlyMissing))
	}
	if r.BacklogRunsCreated > 0 {
		parts = append(parts, fmt.Sprintf("%d backlog runs", r.BacklogRunsCreated))
	}
	if r.WantListPruned > 0 {
		parts = append(parts, fmt.Sprintf("pruned %d want", r.WantListPruned))
	}
	if r.SeriesMerged > 0 {
		parts = append(parts, fmt.Sprintf("merged %d series", r.SeriesMerged))
	}
	if r.IssuesMerged > 0 {
		parts = append(parts, fmt.Sprintf("merged %d issues", r.IssuesMerged))
	}
	if r.PostersWritten > 0 {
		parts = append(parts, fmt.Sprintf("wrote %d posters", r.PostersWritten))
	}
	if r.Errors > 0 {
		parts = append(parts, fmt.Sprintf("%d errors", r.Errors))
	}
	return "Scan complete: " + strings.Join(parts, ", ")
}

// reconcileDiskVsDB drops every comic_files row whose file_path no longer
// resolves on disk. issue.has_file is computed via JOIN and recovers
// automatically. Returns the number of rows removed.
//
// `present` is an optional set of file paths the caller already discovered
// during the scan walk — when supplied, this avoids an os.Stat per row,
// which on a network share (SMB) can be 5–50ms each and dominate scan
// time. nil means fall back to per-row os.Stat.
func (s *LibraryService) reconcileDiskVsDB(ctx context.Context, present map[string]struct{}) (int, error) {
	files, err := s.fileRepo.ListAll()
	if err != nil {
		return 0, fmt.Errorf("listing files: %w", err)
	}

	removed := 0
	clearedSeriesCovers := make(map[int64]int64) // seriesID -> ghost fileID it referenced

	for _, f := range files {
		select {
		case <-ctx.Done():
			return removed, ctx.Err()
		default:
		}

		if present != nil {
			if _, ok := present[f.FilePath]; ok {
				continue
			}
		} else if _, err := os.Stat(f.FilePath); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			slog.Warn("stat failed during reconcile", "path", f.FilePath, "error", err)
			continue
		}

		// File is gone — clean up associated cover thumbnail and the row itself.
		if f.CoverPath != "" {
			if err := os.Remove(f.CoverPath); err != nil && !os.IsNotExist(err) {
				slog.Debug("could not remove orphaned cover", "path", f.CoverPath, "error", err)
			}
		}
		if f.IssueID != nil {
			if issue, err := s.issueRepo.GetByID(*f.IssueID); err == nil && issue != nil {
				clearedSeriesCovers[issue.SeriesID] = f.ID
			}
		}
		if err := s.fileRepo.Delete(f.ID); err != nil {
			slog.Warn("failed to delete orphaned file row", "id", f.ID, "path", f.FilePath, "error", err)
			continue
		}
		removed++
	}

	// If a deleted file was a series' chosen cover, clear the pointer so
	// the next read finds another file (or nothing).
	for seriesID, fileID := range clearedSeriesCovers {
		series, err := s.seriesRepo.GetByID(seriesID)
		if err != nil || series == nil {
			continue
		}
		if series.CoverFileID != nil && *series.CoverFileID == fileID {
			if err := s.seriesRepo.ClearCoverFileID(seriesID); err != nil {
				slog.Debug("failed to clear stale series cover", "series_id", seriesID, "error", err)
			}
		}
	}

	return removed, nil
}

type cvReconcileResult struct {
	seriesRefreshed    int
	newlyMissing       int
	backlogRunsCreated int
}

// reconcileDBVsCV refreshes ComicVine metadata for tracked series whose
// last_cv_sync is older than the configured TTL, then (if scan_auto_queue_backlog
// is enabled) creates a backlog run for each series with newly-detected gaps.
func (s *LibraryService) reconcileDBVsCV(ctx context.Context, opts ScanOptions, progress ScanProgressFunc) (cvReconcileResult, error) {
	res := cvReconcileResult{}

	if s.metaSvc == nil || !s.metaSvc.HasAPIKey() {
		return res, nil
	}

	tracked, err := s.seriesRepo.ListTracked()
	if err != nil {
		return res, fmt.Errorf("listing tracked series: %w", err)
	}
	if len(tracked) == 0 {
		return res, nil
	}

	autoQueue := readScanAutoQueueBacklog(s.settingRepo)
	// When ForceCV is set the cutoff is "now," which causes every recorded
	// last_cv_sync to fall before it and refresh.
	var cutoff time.Time
	if opts.ForceCV {
		cutoff = time.Now().UTC().Add(time.Second)
	} else {
		ttlHours := readScanCVRefreshTTL(s.settingRepo)
		cutoff = time.Now().UTC().Add(-time.Duration(ttlHours) * time.Hour)
	}

	totalTracked := len(tracked)
	for i, series := range tracked {
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		default:
		}

		if series.ComicVineID == nil {
			continue
		}
		if !shouldRefreshCV(stringPtrValue(series.LastCVSync), cutoff) {
			continue
		}

		// Hard short-circuit when BOTH providers are out of quota.
		// Without this the loop calls RefreshSeriesAuto which dives into a
		// rate-limiter Wait that sleeps until the hourly reset (~50 min)
		// — every subsequent series then waits another reset, so a scan
		// with 200 tracked series can take literal days. Defer instead:
		// finish the scan, the user re-runs after quota replenishes.
		cvLeft := s.metaSvc.HourlyRemaining()
		metronExhausted := !s.metaSvc.HasMetronCredentials()
		if !metronExhausted {
			mq := s.metaSvc.MetronQuota()
			metronExhausted = mq.SustainedRemaining <= 0 || mq.BurstRemaining <= 0
		}
		// Defer when CV is effectively spent (≤1 — the next call will
		// drain it and the one after that has to wait the full hourly
		// reset, possibly an hour-plus per series). Combined with Metron
		// exhausted/unconfigured, that means the rest of the loop is
		// going to block for hours per call. Earlier iteration of this
		// guard required cvLeft<=0 strictly, which left scans wedged on
		// "quota: 1 remaining" for 30+ minutes.
		const cvDeferThreshold = 1
		if cvLeft <= cvDeferThreshold && metronExhausted {
			deferred := 0
			for j := i; j < len(tracked); j++ {
				if tracked[j].ComicVineID != nil || tracked[j].MetronID != nil {
					deferred++
				}
			}
			eta := s.metaSvc.CVNextResetIn().Round(time.Minute)
			if eta < time.Minute {
				eta = time.Minute
			}
			if progress != nil {
				progress(i, totalTracked, fmt.Sprintf(
					"Both CV + Metron quotas exhausted — deferred %d series (CV resets in %s); re-run scan later",
					deferred, eta))
			}
			slog.Warn("scan: CV+Metron quotas both exhausted, deferring remaining series",
				"deferred", deferred, "cv_reset_eta", eta)
			break
		}

		if progress != nil {
			if cvLeft <= 0 {
				progress(i, totalTracked, fmt.Sprintf("CV exhausted; refreshing %s via Metron", series.Title))
			} else {
				progress(i, totalTracked, fmt.Sprintf("CV: refreshing %s (quota: %d remaining)", series.Title, cvLeft))
			}
		}

		// Snapshot pre-refresh missing count for delta detection.
		preMissing := s.countMissing(series.ID)

		if err := s.metaSvc.RefreshSeriesAuto(ctx, series.ID); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return res, err
			}
			slog.Warn("metadata refresh failed during scan", "series_id", series.ID, "title", series.Title, "error", err)
			continue
		}
		res.seriesRefreshed++

		postMissing := s.countMissing(series.ID)
		newGaps := postMissing - preMissing
		if newGaps > 0 {
			res.newlyMissing += newGaps
			if autoQueue && s.backlogSvc != nil {
				if _, err := s.backlogSvc.CreateRun(series.ID, nil); err != nil {
					slog.Warn("auto-queue backlog failed", "series_id", series.ID, "error", err)
				} else {
					res.backlogRunsCreated++
				}
			}
		}
	}

	return res, nil
}

// countMissing returns the number of issues in a series that would qualify
// for the backlog (no local file, no skip status).
func (s *LibraryService) countMissing(seriesID int64) int {
	issues, err := s.issueRepo.ListBySeries(seriesID)
	if err != nil {
		return 0
	}
	count := 0
	for _, i := range issues {
		if i.HasFile {
			continue
		}
		if i.SkipStatus != nil {
			continue
		}
		count++
	}
	return count
}

// shouldRefreshCV reports whether a series' last CV sync is older than cutoff,
// or whether it has never been synced. Empty lastSync means "never".
func shouldRefreshCV(lastSync string, cutoff time.Time) bool {
	if lastSync == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, lastSync)
	if err != nil {
		return true
	}
	return t.Before(cutoff)
}

func stringPtrValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func readScanCVRefreshTTL(settingRepo *repository.SettingRepo) int {
	if settingRepo == nil {
		return 24
	}
	v, _ := settingRepo.Get("scan_cv_refresh_ttl_hours")
	hours, err := strconv.Atoi(v)
	if err != nil || hours < 1 || hours > 24*30 {
		return 24
	}
	return hours
}

func readScanAutoQueueBacklog(settingRepo *repository.SettingRepo) bool {
	if settingRepo == nil {
		return false
	}
	v, _ := settingRepo.Get("scan_auto_queue_backlog")
	return v == "true"
}

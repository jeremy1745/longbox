package service

// acquisition.go is the orchestrator for the "Want+Track full-acquisition
// flow." A single call to WantAndTrackSeries takes a ComicVine or Metron
// identifier and drives the whole pipeline:
//
//  1. resolve or create the local series (propagating typed match conflicts)
//  2. mark the series tracked and every issue wanted + pending
//  3. create the on-disk series folder + poster and write metadata sidecars
//  4. scan the local library for loose files of this series and move them in
//  5. queue every still-missing issue for download via Prowlarr
//
// Steps 3-5 are best-effort: a failure there is recorded in
// WantTrackResult.Warnings and the flow continues. Only a hard failure in
// step 1 (series resolution) aborts the call. Match-conflict errors from
// step 1 are propagated UNWRAPPED so the HTTP handler can detect them with
// errors.As and return a 409.

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/prowlarr"
	"github.com/jeremy/longbox/internal/repository"
)

// WantTrackInput identifies the series to acquire. Exactly one of ComicVineID
// or MetronID should be set. SourceIssueID is the issue the user clicked from
// a pull-list "+" — informational only; the flow wants ALL issues of the
// series, not a partial range.
type WantTrackInput struct {
	ComicVineID   *int64
	MetronID      *int64
	SourceIssueID *int64
}

// WantTrackResult summarizes what WantAndTrackSeries did.
type WantTrackResult struct {
	SeriesID        int64    `json:"series_id"`
	FolderPath      string   `json:"folder_path"`
	MetadataWritten bool     `json:"metadata_written"`
	FilesMoved      int      `json:"files_moved"`
	IssuesQueued    int      `json:"issues_queued"`
	Warnings        []string `json:"warnings,omitempty"`
}

// seriesResolver is the narrow slice of *MetadataService that the
// acquisition flow needs to resolve/create a series. Defined here as an
// interface so the integration test can substitute a fake that doesn't hit
// the ComicVine / Metron network.
//
// NOTE: this deliberately does NOT include a GetLibraryDir method.
// *MetadataService has no such method — the library directory lives in the
// settings DB / config and is resolved once by the Phase 6 wiring, then
// passed to NewAcquisitionService as a plain string. Reaching through the
// metadata service for a filesystem path would be the wrong seam.
type seriesResolver interface {
	// TrackFromComicVine resolves or creates a CV-matched series and
	// populates its issues. wantAll is left false here — acquisition does
	// its own want-list handling.
	TrackFromComicVine(cvVolumeID int, wantListRepo *repository.WantListRepo, wantAll ...bool) (*model.Series, int, error)
	// MatchSeriesToMetronVolume binds an EXISTING local series row to a
	// Metron series and populates issues.
	MatchSeriesToMetronVolume(ctx context.Context, seriesID int64, metronSeriesID int) error
}

// releaseGrabber is the narrow slice of *prowlarr.Client the flow needs.
// *prowlarr.Client satisfies this structurally.
type releaseGrabber interface {
	SearchIssue(ctx context.Context, series, issueNumber string, year int) ([]prowlarr.Release, error)
	GrabRelease(ctx context.Context, guid string, indexerID int) error
}

// folderEnsurer is the narrow slice of *SeriesFolderService the flow needs.
// Defined as an interface so the test can avoid constructing the full
// LibraryService / CoverService graph and the HTTP cover download it does.
// *SeriesFolderService satisfies this structurally.
type folderEnsurer interface {
	EnsureFolderAndPoster(ctx context.Context, seriesID int64) error
}

// AcquisitionService ties the resolution, tracking, folder/sidecar, local
// file-move, and Prowlarr-queue steps into one orchestrated flow.
type AcquisitionService struct {
	resolver        seriesResolver
	seriesFolderSvc folderEnsurer
	libraryScanSvc  *LibraryScanService
	prowlarrClient  releaseGrabber // may be nil — step 5 is skipped when so
	seriesRepo      *repository.SeriesRepo
	issueRepo       *repository.IssueRepo
	fileRepo        *repository.FileRepo
	wantListRepo    *repository.WantListRepo
	libraryDir      string // resolved once by the caller (settings DB / config)
}

// NewAcquisitionService constructs an AcquisitionService. prowlarrClient may
// be nil when Prowlarr isn't configured — the queue step is then skipped.
// libraryDir is the resolved library root; the Phase 6 wiring reads it from
// the settings DB (config fallback) and passes it in.
func NewAcquisitionService(
	resolver seriesResolver,
	seriesFolderSvc folderEnsurer,
	libraryScanSvc *LibraryScanService,
	prowlarrClient releaseGrabber,
	seriesRepo *repository.SeriesRepo,
	issueRepo *repository.IssueRepo,
	fileRepo *repository.FileRepo,
	wantListRepo *repository.WantListRepo,
	libraryDir string,
) *AcquisitionService {
	return &AcquisitionService{
		resolver:        resolver,
		seriesFolderSvc: seriesFolderSvc,
		libraryScanSvc:  libraryScanSvc,
		prowlarrClient:  prowlarrClient,
		seriesRepo:      seriesRepo,
		issueRepo:       issueRepo,
		fileRepo:        fileRepo,
		wantListRepo:    wantListRepo,
		libraryDir:      libraryDir,
	}
}

// WantAndTrackSeries runs the full acquisition flow described in the file
// header. It returns a populated WantTrackResult on success. The only error
// it returns is a hard failure in step 1 (series resolution) — and when that
// failure is a *CVMatchConflictError / *MetronMatchConflictError /
// *SeriesMatchConflictError it is returned so errors.As still unwraps to the
// typed error.
func (s *AcquisitionService) WantAndTrackSeries(ctx context.Context, in WantTrackInput) (WantTrackResult, error) {
	var result WantTrackResult

	// ── Step 1: resolve or create the series ──────────────────────────────
	series, err := s.resolveSeries(ctx, in)
	if err != nil {
		// Propagate UNWRAPPED (or %w-wrapped) so the Phase 6 handler's
		// errors.As-based writeMatchConflict helper still works.
		return result, err
	}
	result.SeriesID = series.ID

	if in.SourceIssueID != nil {
		slog.Debug("acquisition: source issue is informational only — wanting all issues of the series",
			"series_id", series.ID, "source_issue_id", *in.SourceIssueID)
	}

	// ── Step 2: mark tracked + want every issue ───────────────────────────
	if err := s.seriesRepo.SetTracked(series.ID, true); err != nil {
		// Tracking is core to the flow's intent; treat a failure as a
		// warning rather than aborting — the series is resolved and the
		// rest of the flow still adds value.
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("could not mark series tracked: %v", err))
	}

	issues, err := s.issueRepo.ListBySeries(series.ID)
	if err != nil {
		// ListBySeries failure guts steps 3/4/5 — nothing to want, nothing
		// to scan against, nothing to queue. Callers seeing err==nil
		// reasonably expect the flow to have run; return a real error.
		return result, fmt.Errorf("listing series issues: %w", err)
	}

	for i := range issues {
		// Check for context cancellation at the top of each iteration so a
		// cancelled request stops promptly rather than walking all issues.
		if err := ctx.Err(); err != nil {
			return result, err
		}
		issueID := issues[i].ID
		if _, err := s.wantListRepo.Create(issueID, 0, ""); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("issue %s: could not add to want list: %v", issues[i].IssueNumber, err))
			continue
		}
		if err := s.wantListRepo.SetProcurementStatus(issueID, "pending", ""); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("issue %s: could not set procurement status pending: %v", issues[i].IssueNumber, err))
		}
	}

	// ── Step 3: create folder + write metadata sidecars ───────────────────
	seriesDir := s.seriesFolderPath(series)
	if err := s.seriesFolderSvc.EnsureFolderAndPoster(ctx, series.ID); err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("could not create series folder/poster: %v", err))
	} else {
		result.FolderPath = seriesDir
		if err := WriteSeriesSidecar(seriesDir, series, issues); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("could not write metadata sidecars: %v", err))
		} else {
			result.MetadataWritten = true
		}
	}

	// ── Step 4: scan local library + move matching files in ───────────────
	filesByIssueNumber := s.moveLocalFiles(s.libraryDir, seriesDir, series, issues, &result)

	// ── Step 5: queue still-missing issues via Prowlarr ───────────────────
	s.queueMissingIssues(ctx, series, issues, filesByIssueNumber, &result)

	slog.Info("acquisition: want+track complete",
		"series_id", series.ID,
		"title", series.Title,
		"files_moved", result.FilesMoved,
		"issues_queued", result.IssuesQueued,
		"warnings", len(result.Warnings),
	)
	return result, nil
}

// resolveSeries handles step 1 — turning the input identifier into a local
// series row. Conflict errors are returned as-is so errors.As still works.
func (s *AcquisitionService) resolveSeries(ctx context.Context, in WantTrackInput) (*model.Series, error) {
	switch {
	case in.ComicVineID != nil:
		// wantAll=false: acquisition does its own want-list handling in step 2.
		series, _, err := s.resolver.TrackFromComicVine(int(*in.ComicVineID), nil, false)
		if err != nil {
			// TrackFromComicVine wraps MatchSeriesToVolume's error with %w,
			// so a CV/Series conflict still unwraps via errors.As. Return
			// it untouched.
			return nil, fmt.Errorf("resolving series from ComicVine: %w", err)
		}
		if series == nil {
			return nil, fmt.Errorf("resolving series from ComicVine: resolver returned nil series")
		}
		return series, nil

	case in.MetronID != nil:
		// There is no single "track from Metron" entrypoint:
		// MatchSeriesToMetronVolume requires the series row to already
		// exist. So we create a minimal placeholder row and let the match
		// populate it — the same create-then-match shape TrackFromComicVine
		// uses internally for ComicVine. The placeholder title is replaced
		// by MatchSeriesToMetronVolume; if the match fails we delete the
		// orphan row so a retry isn't blocked by a junk series.
		placeholder := &model.Series{
			Title:  fmt.Sprintf("metron-%d", *in.MetronID),
			Status: "continuing",
		}
		if err := s.seriesRepo.Create(placeholder); err != nil {
			return nil, fmt.Errorf("resolving series from Metron: creating placeholder series: %w", err)
		}
		if err := s.resolver.MatchSeriesToMetronVolume(ctx, placeholder.ID, int(*in.MetronID)); err != nil {
			// Roll back the placeholder so the failed attempt leaves no
			// junk row. A conflict error here references the placeholder's
			// ID, which is fine — the handler reports the conflicting
			// EXISTING series, not the placeholder.
			if delErr := s.seriesRepo.Delete(placeholder.ID); delErr != nil {
				slog.Warn("acquisition: could not delete placeholder series after failed Metron match",
					"series_id", placeholder.ID, "error", delErr)
			}
			return nil, fmt.Errorf("resolving series from Metron: %w", err)
		}
		series, err := s.seriesRepo.GetByID(placeholder.ID)
		if err != nil {
			// Successful match but reload failed — delete the placeholder so
			// no orphaned row is left behind. Same cleanup as match-failure.
			if delErr := s.seriesRepo.Delete(placeholder.ID); delErr != nil {
				slog.Warn("acquisition: could not delete placeholder series after failed reload",
					"series_id", placeholder.ID, "error", delErr)
			}
			return nil, fmt.Errorf("resolving series from Metron: reloading series: %w", err)
		}
		if series == nil {
			// Row vanished between match and reload — same cleanup.
			if delErr := s.seriesRepo.Delete(placeholder.ID); delErr != nil {
				slog.Warn("acquisition: could not delete placeholder series after nil reload",
					"series_id", placeholder.ID, "error", delErr)
			}
			return nil, fmt.Errorf("resolving series from Metron: series %d vanished after match", placeholder.ID)
		}
		return series, nil

	default:
		return nil, fmt.Errorf("want+track: input has neither ComicVineID nor MetronID")
	}
}

// seriesFolderPath derives the canonical on-disk folder for a series — the
// same path SeriesFolderService.EnsureFolderAndPoster writes to: the library
// dir joined with buildSeriesFolderName(title, year).
func (s *AcquisitionService) seriesFolderPath(series *model.Series) string {
	return filepath.Join(s.libraryDir, buildSeriesFolderName(series.Title, series.Year))
}

// moveLocalFiles handles step 4 — scanning the library for loose files of
// this series and moving them into the canonical folder. It returns the set
// of normalized issue numbers that ended up with a local file (so step 5 can
// skip them). Every failure mode is recorded as a warning; nothing here is
// fatal.
//
// issues is the full slice returned by ListBySeries in step 2. We resolve
// scan keys → issue rows here, via a normalized-comparison map, rather than
// going back to the DB with an exact string query — that would silently miss
// zero-padded issue numbers (DB "001" vs scan key "1").
func (s *AcquisitionService) moveLocalFiles(
	libraryDir, seriesDir string,
	series *model.Series,
	issues []model.Issue,
	result *WantTrackResult,
) map[string]bool {
	filesByIssueNumber := make(map[string]bool)

	if libraryDir == "" {
		result.Warnings = append(result.Warnings,
			"library dir is not configured — skipping local file scan")
		return filesByIssueNumber
	}

	scan, err := s.libraryScanSvc.FindFilesForSeries(libraryDir, series)
	if err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("local library scan failed: %v", err))
		return filesByIssueNumber
	}

	// Build a normalized-key lookup map once so relocateFile doesn't need a
	// DB round-trip per file. Both the scan keys AND the DB values are
	// normalised the same way, so "001" and "1" both map to "1".
	issueByNorm := make(map[string]*model.Issue, len(issues))
	for i := range issues {
		key := normalizeIssueNumber(issues[i].IssueNumber)
		issueByNorm[key] = &issues[i]
	}

	// Regular issues land in seriesDir; annuals under seriesDir/Annuals.
	for issueNumber, srcPath := range scan.Matches {
		dest := filepath.Join(seriesDir, filepath.Base(srcPath))
		if s.relocateFile(issueNumber, srcPath, dest, issueByNorm, result) {
			filesByIssueNumber[issueNumber] = true
		}
	}
	for issueNumber, srcPath := range scan.Annuals {
		dest := filepath.Join(seriesDir, "Annuals", filepath.Base(srcPath))
		if s.relocateFile(issueNumber, srcPath, dest, issueByNorm, result) {
			filesByIssueNumber[issueNumber] = true
		}
	}

	for _, rejected := range scan.RejectedDuplicates {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("rejected duplicate file (not moved): %s", rejected))
	}

	return filesByIssueNumber
}

// relocateFile moves one scanned file into the series folder and relinks the
// comic_files DB row. Returns true if the file ended up in place (whether by
// move or because it was already there) so the caller can mark the issue as
// having a local file. Mirrors FileOrganizerService.RenameForIssue's
// rename → DB-update → rollback-on-DB-failure pattern.
//
// issueByNorm maps normalizeIssueNumber(issue.IssueNumber) → *model.Issue for
// every issue in the series. Using a pre-built normalized map avoids a
// FindBySeriesAndNumber DB round-trip that would silently miss zero-padded
// issue numbers stored in the DB (e.g., DB "001" vs scan key "1").
func (s *AcquisitionService) relocateFile(
	issueNumber, srcPath, dest string,
	issueByNorm map[string]*model.Issue,
	result *WantTrackResult,
) bool {
	// issueNumber is already normalized by FindFilesForSeries; look it up
	// directly in the pre-built normalized map.
	issue, ok := issueByNorm[issueNumber]
	if !ok {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("issue %s: no local issue row, file %s not moved", issueNumber, filepath.Base(srcPath)))
		return false
	}

	// Already in its canonical slot — nothing to move, but the issue does
	// have a local file. Note: return true here WITHOUT incrementing
	// result.FilesMoved — the bool signals "issue has a file" while
	// FilesMoved counts actual disk moves performed by this run.
	if filepath.Clean(srcPath) == filepath.Clean(dest) {
		return true
	}

	if _, err := os.Stat(dest); err == nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("issue %s: destination already exists, file %s not moved: %s", issueNumber, filepath.Base(srcPath), dest))
		return false
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("issue %s: could not create destination dir, file %s not moved: %v", issueNumber, filepath.Base(srcPath), err))
		return false
	}

	if err := os.Rename(srcPath, dest); err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("issue %s: move failed for %s: %v", issueNumber, filepath.Base(srcPath), err))
		return false
	}

	// Relink the comic_files row, if one exists. A missing row is not an
	// error — the file was never scanned in; the existing library scan /
	// reattach pass will create the row later.
	file, err := s.fileRepo.GetByPath(srcPath)
	if err != nil {
		// Roll the move back: we can't safely leave the DB and disk
		// disagreeing on a row we know exists-or-not ambiguously.
		if rbErr := os.Rename(dest, srcPath); rbErr != nil {
			slog.Error("acquisition: rollback failed after GetByPath error",
				"src", srcPath, "dest", dest, "error", rbErr)
		}
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("issue %s: DB lookup failed for moved file, rolled back: %v", issueNumber, err))
		return false
	}
	if file == nil {
		// File was never scanned into the DB — leave it moved on disk.
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("INFO issue %s: moved %s into place; no comic_files row yet (library scan will attach it)", issueNumber, filepath.Base(dest)))
		result.FilesMoved++
		return true
	}

	newName := filepath.Base(dest)
	if err := s.fileRepo.UpdatePath(file.ID, dest, newName); err != nil {
		if rbErr := os.Rename(dest, srcPath); rbErr != nil {
			slog.Error("acquisition: rollback failed after UpdatePath error",
				"file_id", file.ID, "actual_path", dest, "expected_path", srcPath, "error", rbErr)
		}
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("issue %s: DB path update failed for %s, rolled back: %v", issueNumber, newName, err))
		return false
	}
	if err := s.fileRepo.UpdateIssueID(file.ID, issue.ID); err != nil {
		// Path was updated but the issue relink failed. Roll the rename
		// back and also restore the path so DB and disk agree again.
		if rbErr := os.Rename(dest, srcPath); rbErr != nil {
			slog.Error("acquisition: rollback failed after UpdateIssueID error",
				"file_id", file.ID, "actual_path", dest, "expected_path", srcPath, "error", rbErr)
		} else if pErr := s.fileRepo.UpdatePath(file.ID, srcPath, filepath.Base(srcPath)); pErr != nil {
			slog.Error("acquisition: could not restore DB path after UpdateIssueID rollback",
				"file_id", file.ID, "expected_path", srcPath, "error", pErr)
		}
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("issue %s: DB issue relink failed for %s, rolled back: %v", issueNumber, newName, err))
		return false
	}

	result.FilesMoved++
	slog.Debug("acquisition: moved local file into series folder",
		"issue", issueNumber, "from", srcPath, "to", dest)
	return true
}

// queueMissingIssues handles step 5 — for every issue without a local file,
// search Prowlarr and grab the first result. Per-issue failures are recorded
// and the loop continues. When prowlarrClient is nil the whole step is
// skipped with a single warning.
func (s *AcquisitionService) queueMissingIssues(
	ctx context.Context,
	series *model.Series,
	issues []model.Issue,
	filesByIssueNumber map[string]bool,
	result *WantTrackResult,
) {
	if s.prowlarrClient == nil {
		result.Warnings = append(result.Warnings,
			"Prowlarr is not configured — skipping download queue step")
		return
	}

	year := 0
	if series.Year != nil {
		year = *series.Year
	}

	for i := range issues {
		// Check for context cancellation at the top of each iteration so a
		// cancelled request stops promptly rather than walking all issues.
		if err := ctx.Err(); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("context cancelled during Prowlarr queue step: %v", err))
			return
		}

		issue := &issues[i]

		// Skip issues that got a local file in step 4.
		if filesByIssueNumber[normalizeIssueNumber(issue.IssueNumber)] {
			continue
		}
		// Also skip issues that already have a comic_files row from before.
		if existing, err := s.fileRepo.GetByIssueID(issue.ID); err == nil && existing != nil {
			continue
		}

		releases, err := s.prowlarrClient.SearchIssue(ctx, series.Title, issue.IssueNumber, year)
		if err != nil {
			s.markProcurementFailed(issue, fmt.Sprintf("prowlarr search failed: %v", err), result)
			continue
		}
		if len(releases) == 0 {
			s.markProcurementFailed(issue, "no results", result)
			continue
		}

		// "Best" = first result. Real ranking is out of scope for this
		// phase (decision D2 — SearchService scoring stays separate).
		best := releases[0]
		if err := s.prowlarrClient.GrabRelease(ctx, best.GUID, best.IndexerID); err != nil {
			s.markProcurementFailed(issue, fmt.Sprintf("prowlarr grab failed: %v", err), result)
			continue
		}

		if err := s.wantListRepo.SetProcurementStatus(issue.ID, "submitted", ""); err != nil {
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("issue %s: grabbed release but could not mark submitted: %v", issue.IssueNumber, err))
			// The grab succeeded — still count it as queued.
		}
		result.IssuesQueued++
		slog.Debug("acquisition: queued issue via Prowlarr",
			"issue", issue.IssueNumber, "guid", best.GUID, "indexer_id", best.IndexerID)
	}
}

// markProcurementFailed records a per-issue Prowlarr failure: sets the want
// list row's procurement_status to "failed" with the error message and
// appends a warning. Never aborts the caller's loop.
func (s *AcquisitionService) markProcurementFailed(issue *model.Issue, reason string, result *WantTrackResult) {
	if err := s.wantListRepo.SetProcurementStatus(issue.ID, "failed", reason); err != nil {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("issue %s: %s (and could not record failed status: %v)", issue.IssueNumber, reason, err))
		return
	}
	result.Warnings = append(result.Warnings,
		fmt.Sprintf("issue %s: %s", issue.IssueNumber, reason))
}

// RetryIssue re-dispatches a single want-list item through the Prowlarr
// search→grab→SetProcurementStatus flow. It is a targeted retry for the
// POST /wantlist/{id}/retry handler and mirrors the per-issue logic in
// queueMissingIssues. Returns the updated WantListItem on success, or an
// error if the issue/series cannot be loaded or Prowlarr isn't configured.
func (s *AcquisitionService) RetryIssue(ctx context.Context, issueID int64) (*model.WantListItem, error) {
	if s.prowlarrClient == nil {
		return nil, fmt.Errorf("prowlarr is not configured")
	}

	issue, err := s.issueRepo.GetByID(issueID)
	if err != nil {
		return nil, fmt.Errorf("retry: loading issue %d: %w", issueID, err)
	}
	if issue == nil {
		return nil, fmt.Errorf("retry: issue %d not found", issueID)
	}

	series, err := s.seriesRepo.GetByID(issue.SeriesID)
	if err != nil {
		return nil, fmt.Errorf("retry: loading series %d: %w", issue.SeriesID, err)
	}
	if series == nil {
		return nil, fmt.Errorf("retry: series %d not found", issue.SeriesID)
	}

	year := 0
	if series.Year != nil {
		year = *series.Year
	}

	// A failed procurement is a NORMAL outcome — it's recorded on the want_list
	// row as procurement_status='failed' with the reason in procurement_last_error.
	// Only infrastructure failures (above) return an error. A search/grab miss
	// returns the reloaded item so the caller renders the updated 'failed' state
	// instead of seeing a 500 and a stale row.
	releases, searchErr := s.prowlarrClient.SearchIssue(ctx, series.Title, issue.IssueNumber, year)
	switch {
	case searchErr != nil:
		_ = s.wantListRepo.SetProcurementStatus(issueID, "failed", fmt.Sprintf("prowlarr search failed: %v", searchErr))
	case len(releases) == 0:
		_ = s.wantListRepo.SetProcurementStatus(issueID, "failed", "no results")
	default:
		best := releases[0]
		if grabErr := s.prowlarrClient.GrabRelease(ctx, best.GUID, best.IndexerID); grabErr != nil {
			_ = s.wantListRepo.SetProcurementStatus(issueID, "failed", fmt.Sprintf("prowlarr grab failed: %v", grabErr))
		} else if err := s.wantListRepo.SetProcurementStatus(issueID, "submitted", ""); err != nil {
			slog.Warn("retry: grab succeeded but could not mark submitted", "issue_id", issueID, "error", err)
		}
	}

	item, err := s.wantListRepo.GetByIssueID(issueID)
	if err != nil {
		return nil, fmt.Errorf("retry: reloading want list item: %w", err)
	}
	return item, nil
}

// Compile-time assertions that the concrete production types satisfy the
// narrow interfaces this orchestrator depends on. These do not run any code;
// they just fail the build if a signature drifts.
var (
	_ seriesResolver = (*MetadataService)(nil)
	_ releaseGrabber = (*prowlarr.Client)(nil)
	_ folderEnsurer  = (*SeriesFolderService)(nil)
)

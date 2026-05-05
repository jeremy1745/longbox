package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	tmpl "github.com/jeremy/longbox/internal/template"
	"github.com/jeremy/longbox/internal/util/trash"
)

const settingKeyNamingTemplate = "naming_template"

// RenamePreview describes a planned file rename.
type RenamePreview struct {
	FileID      int64  `json:"file_id"`
	CurrentPath string `json:"current_path"`
	NewPath     string `json:"new_path"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
}

// RenameResult summarizes the outcome of an organize operation.
type RenameResult struct {
	TotalFiles   int      `json:"total_files"`
	Moved        int      `json:"moved"`
	Skipped      int      `json:"skipped"`
	Errors       int      `json:"errors"`
	ErrorDetails []string `json:"error_details,omitempty"`
}

// FileOrganizerService handles template-based file organization.
type FileOrganizerService struct {
	fileRepo     *repository.FileRepo
	issueRepo    *repository.IssueRepo
	seriesRepo   *repository.SeriesRepo
	settingRepo  *repository.SettingRepo
	annualFolder string
}

func NewFileOrganizerService(
	fileRepo *repository.FileRepo,
	issueRepo *repository.IssueRepo,
	seriesRepo *repository.SeriesRepo,
	settingRepo *repository.SettingRepo,
	annualSubfolder string,
) *FileOrganizerService {
	folder := tmpl.SanitizePathComponent(annualSubfolder)
	if folder == "" {
		folder = "Annuals"
	}
	return &FileOrganizerService{
		fileRepo:     fileRepo,
		issueRepo:    issueRepo,
		seriesRepo:   seriesRepo,
		settingRepo:  settingRepo,
		annualFolder: folder,
	}
}

// GetTemplate returns the current naming template from settings.
func (s *FileOrganizerService) GetTemplate() string {
	t, err := s.settingRepo.Get(settingKeyNamingTemplate)
	if err != nil || t == "" {
		return tmpl.DefaultTemplate
	}
	return t
}

// SetTemplate validates and saves a naming template to settings.
func (s *FileOrganizerService) SetTemplate(template string) error {
	_, err := tmpl.Parse(template)
	if err != nil {
		return fmt.Errorf("invalid template: %w", err)
	}
	return s.settingRepo.Set(settingKeyNamingTemplate, template)
}

// BuildPath renders the naming template for a specific issue/series.
func (s *FileOrganizerService) BuildPath(series *model.Series, issue *model.Issue, format string) (string, error) {
	templateStr := s.GetTemplate()
	t, err := tmpl.Parse(templateStr)
	if err != nil {
		return "", err
	}
	return s.renderPath(t, series, issue, format, nil)
}

// Preview generates a dry-run list of all proposed renames.
func (s *FileOrganizerService) Preview(libraryDir, templateStr string) ([]RenamePreview, error) {
	if templateStr == "" {
		templateStr = s.GetTemplate()
	}

	t, err := tmpl.Parse(templateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid template: %w", err)
	}

	files, err := s.fileRepo.ListAll()
	if err != nil {
		return nil, fmt.Errorf("listing files: %w", err)
	}

	parentCache := make(map[int64]*model.Series)
	var previews []RenamePreview
	newPaths := make(map[string]int64)

	for _, f := range files {
		preview := RenamePreview{FileID: f.ID, CurrentPath: f.FilePath}

		if f.IssueID == nil {
			preview.Status = "unlinked"
			preview.Reason = "File not linked to an issue"
			preview.NewPath = f.FilePath
			previews = append(previews, preview)
			continue
		}

		issue, err := s.issueRepo.GetByID(*f.IssueID)
		if err != nil || issue == nil {
			preview.Status = "unlinked"
			preview.Reason = "Linked issue not found"
			preview.NewPath = f.FilePath
			previews = append(previews, preview)
			continue
		}

		series, err := s.seriesRepo.GetByID(issue.SeriesID)
		if err != nil || series == nil {
			preview.Status = "unlinked"
			preview.Reason = "Series not found"
			preview.NewPath = f.FilePath
			previews = append(previews, preview)
			continue
		}

		relPath, err := s.renderPath(t, series, issue, f.FileFormat, parentCache)
		if err != nil {
			preview.Status = "unlinked"
			preview.Reason = fmt.Sprintf("Template error: %v", err)
			preview.NewPath = f.FilePath
			previews = append(previews, preview)
			continue
		}

		newFullPath := filepath.Join(libraryDir, relPath)
		preview.NewPath = newFullPath

		if filepath.Clean(f.FilePath) == filepath.Clean(newFullPath) {
			preview.Status = "skip"
			preview.Reason = "Already organized"
			previews = append(previews, preview)
			continue
		}

		if existingID, exists := newPaths[newFullPath]; exists {
			preview.Status = "conflict"
			preview.Reason = fmt.Sprintf("Path conflicts with file #%d", existingID)
			previews = append(previews, preview)
			continue
		}

		preview.Status = "move"
		newPaths[newFullPath] = f.ID
		previews = append(previews, preview)
	}

	return previews, nil
}

// Execute is the legacy synchronous form retained for callers that don't
// have a ctx / progress callback handy.
func (s *FileOrganizerService) Execute(libraryDir string) (*RenameResult, error) {
	return s.ExecuteWithProgress(context.Background(), libraryDir, nil)
}

// ExecuteWithProgress moves files according to the current template,
// reporting progress and honoring ctx cancellation. Used by the scheduler
// so the Reorganize job appears in the active-job banner with live
// counts and can be cancelled mid-flight.
func (s *FileOrganizerService) ExecuteWithProgress(
	ctx context.Context,
	libraryDir string,
	progress func(processed, total int, message string),
) (*RenameResult, error) {
	if progress == nil {
		progress = func(int, int, string) {}
	}
	progress(0, 0, "Computing reorganize plan")
	previews, err := s.Preview(libraryDir, "")
	if err != nil {
		return nil, err
	}

	// Total for progress reporting = number of items we're actually going
	// to attempt to move. Skipped/conflict rows are accounted for up
	// front so the UI's percentage tracks real work.
	moveCount := 0
	for _, p := range previews {
		if p.Status == "move" {
			moveCount++
		}
	}

	result := &RenameResult{TotalFiles: len(previews)}
	origDirs := make(map[string]bool)
	processed := 0

	for _, p := range previews {
		select {
		case <-ctx.Done():
			result.ErrorDetails = append(result.ErrorDetails, "cancelled")
			return result, ctx.Err()
		default:
		}

		if p.Status != "move" {
			result.Skipped++
			continue
		}

		processed++
		progress(processed, moveCount, fmt.Sprintf("Moving %s → %s",
			filepath.Base(p.CurrentPath), filepath.Base(p.NewPath)))

		origDirs[filepath.Dir(p.CurrentPath)] = true

		targetDir := filepath.Dir(p.NewPath)
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			result.Errors++
			result.ErrorDetails = append(result.ErrorDetails,
				fmt.Sprintf("Failed to create dir %s: %v", targetDir, err))
			continue
		}

		// Reconcile DB-vs-disk drift before treating "target exists" as a
		// hard error. A previous reorg pass may have physically moved the
		// file but failed to update the DB row (network blip, partial
		// transaction). When that happens every subsequent preview re-flags
		// the same row as a "move", and Execute would silently skip it
		// because the target file already exists. Catch that case: if the
		// target exists AND the source no longer does, the rename clearly
		// happened earlier — just update the DB and count it as moved.
		if _, err := os.Stat(p.NewPath); err == nil {
			// Case 0 — case-only difference. On NTFS / case-insensitive
			// SMB, p.CurrentPath and p.NewPath can resolve to the same
			// physical file (e.g. the dir was indexed as "Beneath the
			// Trees" but the canonical rendering wants "Beneath The
			// Trees"). Treating them as separate would cause the
			// orphan-claim path below to trash the file we're about to
			// rename. Detect by case-insensitive path equality and just
			// update the DB to the canonical casing — the file itself
			// is already where we want it.
			if strings.EqualFold(filepath.Clean(p.CurrentPath), filepath.Clean(p.NewPath)) {
				newName := filepath.Base(p.NewPath)
				if dbErr := s.fileRepo.UpdatePath(p.FileID, p.NewPath, newName); dbErr != nil {
					result.Errors++
					result.ErrorDetails = append(result.ErrorDetails,
						fmt.Sprintf("case-only DB update failed for %s: %v", filepath.Base(p.NewPath), dbErr))
					continue
				}
				result.Moved++
				slog.Info("reconciled DB casing (file already at canonical path, case-only diff)",
					"file_id", p.FileID, "old", p.CurrentPath, "new", p.NewPath)
				continue
			}

			// Case A — DB-vs-disk drift: source missing, target present.
			// A previous reorg moved the file but failed to update the DB.
			// Just point the DB row at the file already on disk.
			if _, srcErr := os.Stat(p.CurrentPath); srcErr != nil && os.IsNotExist(srcErr) {
				newName := filepath.Base(p.NewPath)
				if dbErr := s.fileRepo.UpdatePath(p.FileID, p.NewPath, newName); dbErr != nil {
					result.Errors++
					result.ErrorDetails = append(result.ErrorDetails,
						fmt.Sprintf("DB-vs-disk drift fix failed for %s: %v", filepath.Base(p.NewPath), dbErr))
					continue
				}
				result.Moved++
				slog.Info("reconciled DB to disk (file already at canonical path)",
					"file_id", p.FileID, "path", p.NewPath)
				continue
			}

			// Case B — both source AND target exist. Look up the target in
			// the DB to decide who's authoritative.
			targetRow, lookupErr := s.fileRepo.GetByPath(p.NewPath)
			if lookupErr == nil && targetRow == nil {
				// Target is an on-disk ORPHAN — present but not tracked
				// in comic_files. Most often a leftover from an earlier
				// download or a previous reorg pass that succeeded on
				// disk but failed mid-DB-update. Trash it (OS recycle
				// bin, reversible) and fall through so the source moves
				// into the now-empty canonical slot.
				if trashErr := trash.MoveToTrash(p.NewPath); trashErr != nil {
					result.Errors++
					result.ErrorDetails = append(result.ErrorDetails,
						fmt.Sprintf("Trash orphan %s failed: %v", p.NewPath, trashErr))
					continue
				}
				slog.Info("trashed on-disk orphan to claim canonical slot",
					"path", p.NewPath, "for_file_id", p.FileID)
				// Fall through to os.Rename below.
			} else if lookupErr == nil && targetRow != nil && targetRow.ID == p.FileID {
				// Same DB row points at both paths somehow — treat as
				// drift, update path, and trash the duplicate source.
				newName := filepath.Base(p.NewPath)
				if dbErr := s.fileRepo.UpdatePath(p.FileID, p.NewPath, newName); dbErr != nil {
					result.Errors++
					result.ErrorDetails = append(result.ErrorDetails,
						fmt.Sprintf("self-collision repair %s failed: %v", filepath.Base(p.NewPath), dbErr))
					continue
				}
				if trashErr := trash.MoveToTrash(p.CurrentPath); trashErr != nil {
					slog.Warn("could not trash duplicate source", "path", p.CurrentPath, "error", trashErr)
				}
				result.Moved++
				continue
			} else {
				// Two distinct DB rows want the same canonical path —
				// genuine duplicate-issue case. Leave as a hard error so
				// the user can resolve via Merge Duplicate Issues.
				result.Errors++
				result.ErrorDetails = append(result.ErrorDetails,
					fmt.Sprintf("Target already exists (tracked by file #%d): %s", targetRow.ID, p.NewPath))
				continue
			}
		}

		// Retry rename with backoff. On Windows + SMB the file can be
		// transiently locked by the Search Indexer, antivirus, or any
		// process that briefly opened it after a recent modification.
		// "Access is denied" / "The process cannot access the file"
		// usually clears within a few seconds.
		var renameErr error
		for attempt := 0; attempt < 4; attempt++ {
			if attempt > 0 {
				select {
				case <-ctx.Done():
					return result, ctx.Err()
				case <-time.After(time.Duration(attempt) * time.Second):
				}
			}
			renameErr = os.Rename(p.CurrentPath, p.NewPath)
			if renameErr == nil {
				break
			}
		}
		if renameErr != nil {
			result.Errors++
			result.ErrorDetails = append(result.ErrorDetails,
				fmt.Sprintf("Failed to move %s → %s after retries: %v", filepath.Base(p.CurrentPath), filepath.Base(p.NewPath), renameErr))
			continue
		}

		newName := filepath.Base(p.NewPath)
		if err := s.fileRepo.UpdatePath(p.FileID, p.NewPath, newName); err != nil {
			slog.Error("DB update failed after file move, attempting rollback",
				"file_id", p.FileID, "new_path", p.NewPath, "error", err)
			if rbErr := os.Rename(p.NewPath, p.CurrentPath); rbErr != nil {
				slog.Error("rollback failed! File is at new location but DB has old path",
					"file_id", p.FileID, "actual_path", p.NewPath, "db_path", p.CurrentPath)
			}
			result.Errors++
			result.ErrorDetails = append(result.ErrorDetails,
				fmt.Sprintf("DB update failed for %s: %v", filepath.Base(p.NewPath), err))
			continue
		}

		result.Moved++
		slog.Debug("file moved", "from", p.CurrentPath, "to", p.NewPath)
	}

	for dir := range origDirs {
		cleanEmptyDirs(dir, libraryDir)
	}

	// Full-library sweep for sidecar-only ghost folders left over from
	// previous reorgs (year-folding moved files out but the year-stamped
	// folder kept its cover.jpg / longbox-series.json and stayed alive).
	// Walks every immediate subdirectory of libraryDir and trashes the
	// sidecar-only ones. Bounded to one level deep — the canonical layout
	// is `<series>/<file>` plus `<series>/Annuals/<file>`, so deeper
	// sidecar-only directories (Annuals/) are visited recursively by
	// cleanEmptyDirs once their parent is checked.
	if entries, err := os.ReadDir(libraryDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			cleanEmptyDirs(filepath.Join(libraryDir, e.Name()), libraryDir)
		}
	}

	slog.Info("organize complete",
		"moved", result.Moved,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)

	return result, nil
}

// RenameForIssue renames the file linked to issueID according to the current
// naming template. Returns the old and new on-disk paths. If the rendered
// path equals the current one (or the issue has no file), it's a no-op and
// changed is false. Falls back to a rollback if the DB update fails after
// the OS rename succeeded.
func (s *FileOrganizerService) RenameForIssue(issueID int64, libraryDir string) (oldPath, newPath string, changed bool, err error) {
	file, err := s.fileRepo.GetByIssueID(issueID)
	if err != nil {
		return "", "", false, fmt.Errorf("looking up file for issue: %w", err)
	}
	if file == nil {
		return "", "", false, nil
	}

	issue, err := s.issueRepo.GetByID(issueID)
	if err != nil || issue == nil {
		return "", "", false, fmt.Errorf("looking up issue: %w", err)
	}
	series, err := s.seriesRepo.GetByID(issue.SeriesID)
	if err != nil || series == nil {
		return "", "", false, fmt.Errorf("looking up series: %w", err)
	}

	templateStr := s.GetTemplate()
	t, err := tmpl.Parse(templateStr)
	if err != nil {
		return "", "", false, fmt.Errorf("invalid template: %w", err)
	}

	relPath, err := s.renderPath(t, series, issue, file.FileFormat, nil)
	if err != nil {
		return "", "", false, fmt.Errorf("rendering path: %w", err)
	}
	target := filepath.Join(libraryDir, relPath)
	if filepath.Clean(file.FilePath) == filepath.Clean(target) {
		return file.FilePath, file.FilePath, false, nil
	}

	if _, err := os.Stat(target); err == nil {
		return file.FilePath, target, false, fmt.Errorf("target already exists: %s", target)
	}

	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return file.FilePath, target, false, fmt.Errorf("creating target dir: %w", err)
	}

	if err := os.Rename(file.FilePath, target); err != nil {
		return file.FilePath, target, false, fmt.Errorf("renaming on disk: %w", err)
	}

	newName := filepath.Base(target)
	if err := s.fileRepo.UpdatePath(file.ID, target, newName); err != nil {
		if rbErr := os.Rename(target, file.FilePath); rbErr != nil {
			slog.Error("rollback failed during issue rename",
				"file_id", file.ID, "actual_path", target, "expected_path", file.FilePath)
		}
		return file.FilePath, target, false, fmt.Errorf("DB update failed: %w", err)
	}

	cleanEmptyDirs(filepath.Dir(file.FilePath), libraryDir)
	slog.Info("renamed file via metadata edit",
		"issue_id", issueID, "from", file.FilePath, "to", target)
	return file.FilePath, target, true, nil
}

func (s *FileOrganizerService) renderPath(t *tmpl.Template, series *model.Series, issue *model.Issue, format string, parentCache map[int64]*model.Series) (string, error) {
	if parentCache == nil {
		parentCache = make(map[int64]*model.Series)
	}

	ctx := tmpl.TemplateContext{
		Series:          series.Title,
		SortSeries:      series.SortTitle,
		Number:          issue.IssueNumber,
		Title:           issue.Title,
		Format:          format,
		CoverDate:       issue.CoverDate,
		StoreDate:       issue.StoreDate,
		Publisher:       series.PublisherName,
		AnnualSubfolder: s.annualFolder,
	}

	if issue.Writers != "" {
		ctx.Writers = strings.TrimSpace(strings.SplitN(issue.Writers, ",", 2)[0])
	}
	if issue.Artists != "" {
		ctx.Artists = strings.TrimSpace(strings.SplitN(issue.Artists, ",", 2)[0])
	}
	// `{year}` is the SERIES start year — same value for every issue in a
	// series so a run that crosses calendar years lands in one folder
	// (e.g. "Absolute Flash (2025)" holds every issue regardless of
	// whether the issue's own cover_date is 2025 or 2026). Issue-level
	// year is exposed separately as `{issue_year}` for templates that
	// want per-issue dating in filenames; nothing in the default uses it.
	if series.Year != nil {
		ctx.SeriesYear = fmt.Sprintf("%d", *series.Year)
		ctx.Year = ctx.SeriesYear
	} else if y := extractYear(issue.CoverDate, issue.StoreDate); y != "" {
		// Unmatched series with no recorded start year — fall back to
		// the issue's own date so the folder isn't unlabeled.
		ctx.Year = y
	}
	ctx.IssueYear = extractYear(issue.CoverDate, issue.StoreDate)

	var parentSeries *model.Series
	if series.ParentSeriesID != nil {
		parentSeries = s.lookupParent(*series.ParentSeriesID, parentCache)
		if parentSeries != nil {
			ctx.ParentSeries = parentSeries.Title
		}
	}

	relPath, err := t.Execute(ctx)
	if err != nil {
		return "", err
	}

	if parentSeries != nil {
		// Annuals / specials live under the parent series' folder using
		// the same `<Title> (<Year>)` convention as standalone series so
		// the Annuals/ subfolder lands in the canonical parent shelf
		// rather than next to it.
		parentLabel := parentSeries.Title
		if parentSeries.Year != nil && *parentSeries.Year > 0 {
			parentLabel = fmt.Sprintf("%s (%d)", parentSeries.Title, *parentSeries.Year)
		}
		parentName := tmpl.SanitizePathComponent(parentLabel)
		relPath = filepath.Join(parentName, s.annualFolder, relPath)
	}

	// Strip empty parens left behind when {year} resolves to "" — without
	// this, an unmatched series with no recorded year produces paths like
	// "1776 ()/1776 () 001.cbz". The template can't conditionally suppress
	// literal characters, so we clean up post-render.
	relPath = stripEmptyYearParens(relPath)

	return relPath, nil
}

// stripEmptyYearParens removes leftover " ()" / "()" — only when the
// parens are literally empty (no characters between them, possibly with
// whitespace). Real folder names with nested parens like "Foo (Vol. 2)
// (2025)" stay untouched.
func stripEmptyYearParens(p string) string {
	for {
		next := strings.ReplaceAll(p, " ()", "")
		next = strings.ReplaceAll(next, "()", "")
		if next == p {
			return p
		}
		p = next
	}
}

func (s *FileOrganizerService) lookupParent(id int64, cache map[int64]*model.Series) *model.Series {
	if parent, ok := cache[id]; ok {
		return parent
	}
	parent, err := s.seriesRepo.GetByID(id)
	if err != nil || parent == nil {
		cache[id] = nil
		return nil
	}
	cache[id] = parent
	return parent
}

// sidecarOnlyFiles is the set of filenames produced by LongBox / Mylar
// metadata writers that are NOT real comic content. A directory containing
// only these is treated as empty — the comic files have all moved out, so
// the leftover sidecars get trashed and the folder removed. Without this
// the year-stamped leftover folders ("Foo (2026)" after a year-folding
// reorg moved files into "Foo (2025)") accumulate forever because they
// still hold cover.jpg, folder.jpg, longbox-series.json.
var sidecarOnlyFiles = map[string]bool{
	"cover.jpg":             true,
	"folder.jpg":            true,
	"poster.jpg":            true,
	"series.json":           true,
	"longbox-series.json":   true,
	"longbox-series.txt":    true,
	".ds_store":             true,
	"thumbs.db":             true,
}

// cleanEmptyDirs removes empty (and sidecar-only) directories from bottom
// up, stopping at libraryDir.
func cleanEmptyDirs(dir, stopAt string) {
	stopAt = filepath.Clean(stopAt)
	for {
		dir = filepath.Clean(dir)
		if dir == stopAt || !strings.HasPrefix(dir, stopAt) {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		// Treat as empty when only sidecar files remain. Any actual
		// comic file or unknown content keeps the directory alive.
		realContent := false
		for _, e := range entries {
			if e.IsDir() {
				realContent = true
				break
			}
			if !sidecarOnlyFiles[strings.ToLower(e.Name())] {
				realContent = true
				break
			}
		}
		if realContent {
			return
		}
		// Trash the sidecars first so the directory can be removed.
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if err := os.Remove(path); err != nil {
				slog.Debug("could not remove sidecar before dir cleanup",
					"path", path, "error", err)
				return // bail; can't empty the directory
			}
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

func extractYear(values ...string) string {
	for _, v := range values {
		if len(v) >= 4 {
			year := v[:4]
			if _, err := fmt.Sscanf(year, "%04d", new(int)); err == nil {
				return year
			}
		}
	}
	return ""
}

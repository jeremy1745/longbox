package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/jeremy/longbox/internal/archive"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	tmpl "github.com/jeremy/longbox/internal/template"
)

// ImportService handles post-processing of completed downloads:
// moving files into the library, importing into the DB, and cleaning up.
type ImportService struct {
	librarySvc    *LibraryService
	organizeSvc   *FileOrganizerService
	wantListRepo  *repository.WantListRepo
	dlHistoryRepo *repository.DownloadHistoryRepo
	fileRepo      *repository.FileRepo
	issueRepo     *repository.IssueRepo
	seriesRepo    *repository.SeriesRepo
	settingRepo   *repository.SettingRepo
	libraryDir    string
}

func NewImportService(
	librarySvc *LibraryService,
	organizeSvc *FileOrganizerService,
	wantListRepo *repository.WantListRepo,
	dlHistoryRepo *repository.DownloadHistoryRepo,
	fileRepo *repository.FileRepo,
	issueRepo *repository.IssueRepo,
	seriesRepo *repository.SeriesRepo,
	settingRepo *repository.SettingRepo,
	libraryDir string,
) *ImportService {
	return &ImportService{
		librarySvc:    librarySvc,
		organizeSvc:   organizeSvc,
		wantListRepo:  wantListRepo,
		dlHistoryRepo: dlHistoryRepo,
		fileRepo:      fileRepo,
		issueRepo:     issueRepo,
		seriesRepo:    seriesRepo,
		settingRepo:   settingRepo,
		libraryDir:    libraryDir,
	}
}

// ImportCompletedDownload processes a completed download: finds comic files in
// the storage path, moves them into the library using the naming template,
// imports them into the DB, and cleans up.
func (s *ImportService) ImportCompletedDownload(item *model.DownloadHistoryItem, storagePath string) {
	slog.Info("importing completed download",
		"id", item.ID,
		"nzb", item.NZBName,
		"storage", storagePath,
	)

	if err := s.doImport(item, storagePath); err != nil {
		slog.Error("import failed",
			"id", item.ID,
			"nzb", item.NZBName,
			"error", err,
		)
		if updateErr := s.dlHistoryRepo.UpdateStatus(item.ID, model.DownloadStatusImportFailed, err.Error()); updateErr != nil {
			slog.Error("failed to set import_failed status", "id", item.ID, "error", updateErr)
		}
	}
}

func (s *ImportService) doImport(item *model.DownloadHistoryItem, storagePath string) error {
	// Use the library dir from LibraryService (may be overridden by DB setting)
	libraryDir := s.librarySvc.GetLibraryDir()
	if libraryDir == "" {
		libraryDir = s.libraryDir
	}

	// Find comic files in the storage path
	comicFiles, err := s.findComicFiles(storagePath)
	if err != nil {
		return fmt.Errorf("finding comic files: %w", err)
	}
	if len(comicFiles) == 0 {
		return fmt.Errorf("no comic files found in %s", storagePath)
	}

	slog.Info("found comic files to import", "count", len(comicFiles), "storage", storagePath)

	for _, srcPath := range comicFiles {
		if err := s.importFile(item, srcPath, libraryDir); err != nil {
			slog.Error("failed to import file", "path", srcPath, "error", err)
			// Continue with other files rather than failing the whole batch
			continue
		}
	}

	// Clean up the download directory
	cleanEmptyDirs(storagePath, filepath.Dir(storagePath))

	return nil
}

func (s *ImportService) importFile(item *model.DownloadHistoryItem, srcPath, libraryDir string) error {
	// Determine the target path
	targetPath, err := s.buildTargetPath(item, srcPath, libraryDir)
	if err != nil {
		return fmt.Errorf("building target path: %w", err)
	}

	// Ensure target directory exists
	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("creating target dir: %w", err)
	}

	// Check for conflicts
	if _, err := os.Stat(targetPath); err == nil {
		return fmt.Errorf("target already exists: %s", targetPath)
	}

	// Move the file (try rename first, fall back to copy+delete for cross-device)
	if err := moveFile(srcPath, targetPath); err != nil {
		return fmt.Errorf("moving file: %w", err)
	}

	slog.Info("moved comic file to library",
		"from", srcPath,
		"to", targetPath,
	)

	// Import into the library DB
	scanResult, err := s.librarySvc.ProcessFiles([]string{targetPath})
	if err != nil {
		return fmt.Errorf("processing file: %w", err)
	}

	// Ensure the comic file is linked to the correct issue
	if item.IssueID != nil {
		cf, err := s.fileRepo.GetByPath(targetPath)
		if err == nil && cf != nil {
			if cf.IssueID == nil || *cf.IssueID != *item.IssueID {
				if err := s.fileRepo.UpdateIssueID(cf.ID, *item.IssueID); err != nil {
					slog.Warn("failed to link comic file to issue",
						"file_id", cf.ID,
						"issue_id", *item.IssueID,
						"error", err,
					)
				}
			}
		}

		// Remove from want list
		if err := s.wantListRepo.DeleteByIssueID(*item.IssueID); err != nil {
			slog.Warn("failed to remove from want list",
				"issue_id", *item.IssueID,
				"error", err,
			)
		}
	}

	slog.Info("import complete",
		"path", targetPath,
		"files_added", scanResult.FilesAdded,
		"issue_id", item.IssueID,
	)

	// Run post-process script if configured
	s.runPostProcessScript(targetPath, item)

	return nil
}

// runPostProcessScript executes the user-configured post-processing script after import.
func (s *ImportService) runPostProcessScript(filePath string, item *model.DownloadHistoryItem) {
	if s.settingRepo == nil {
		return
	}

	scriptPath, err := s.settingRepo.Get("post_process_script")
	if err != nil || scriptPath == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, scriptPath)
	cmd.Env = append(os.Environ(),
		"LONGBOX_FILE_PATH="+filePath,
	)

	// Enrich with metadata if available
	if item.IssueID != nil {
		issue, err := s.issueRepo.GetByID(*item.IssueID)
		if err == nil && issue != nil {
			cmd.Env = append(cmd.Env,
				"LONGBOX_ISSUE_NUMBER="+issue.IssueNumber,
			)
			if issue.ComicVineID != nil {
				cmd.Env = append(cmd.Env,
					fmt.Sprintf("LONGBOX_COMICVINE_ID=%d", *issue.ComicVineID),
				)
			}
			series, err := s.seriesRepo.GetByID(issue.SeriesID)
			if err == nil && series != nil {
				cmd.Env = append(cmd.Env,
					"LONGBOX_SERIES="+series.Title,
				)
			}
		}
	}

	// Pass metadata as JSON on stdin
	stdinData := map[string]interface{}{
		"file_path": filePath,
		"nzb_name":  item.NZBName,
	}
	if item.IssueID != nil {
		stdinData["issue_id"] = *item.IssueID
	}
	stdinJSON, _ := json.Marshal(stdinData)
	cmd.Stdin = strings.NewReader(string(stdinJSON))

	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Warn("post-process script failed",
			"script", scriptPath,
			"error", err,
			"output", string(output),
		)
		return
	}

	slog.Info("post-process script completed",
		"script", scriptPath,
		"file", filePath,
	)
}

// buildTargetPath determines where the file should go in the library.
func (s *ImportService) buildTargetPath(item *model.DownloadHistoryItem, srcPath, libraryDir string) (string, error) {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(srcPath), "."))

	// If the download has an issue_id, use the naming template
	if item.IssueID != nil {
		issue, err := s.issueRepo.GetByID(*item.IssueID)
		if err != nil || issue == nil {
			slog.Warn("could not look up issue for naming, using filename", "issue_id", *item.IssueID, "error", err)
			return filepath.Join(libraryDir, filepath.Base(srcPath)), nil
		}

		series, err := s.seriesRepo.GetByID(issue.SeriesID)
		if err != nil || series == nil {
			slog.Warn("could not look up series for naming, using filename", "series_id", issue.SeriesID, "error", err)
			return filepath.Join(libraryDir, filepath.Base(srcPath)), nil
		}

		templateStr := s.organizeSvc.GetTemplate()
		t, err := tmpl.Parse(templateStr)
		if err != nil {
			slog.Warn("invalid naming template, using filename", "error", err)
			return filepath.Join(libraryDir, filepath.Base(srcPath)), nil
		}

		ctx := tmpl.TemplateContext{
			Series:     series.Title,
			SortSeries: series.SortTitle,
			Number:     issue.IssueNumber,
			Title:      issue.Title,
			Format:     ext,
			CoverDate:  issue.CoverDate,
			StoreDate:  issue.StoreDate,
			Publisher:  series.PublisherName,
		}
		if issue.Writers != "" {
			ctx.Writers = strings.TrimSpace(strings.SplitN(issue.Writers, ",", 2)[0])
		}
		if issue.Artists != "" {
			ctx.Artists = strings.TrimSpace(strings.SplitN(issue.Artists, ",", 2)[0])
		}

		relPath, err := t.Execute(ctx)
		if err != nil {
			slog.Warn("template execution failed, using filename", "error", err)
			return filepath.Join(libraryDir, filepath.Base(srcPath)), nil
		}

		return filepath.Join(libraryDir, relPath), nil
	}

	// No issue_id — place directly in libraryDir
	return filepath.Join(libraryDir, filepath.Base(srcPath)), nil
}

// findComicFiles walks the storage path and returns all comic file paths.
func (s *ImportService) findComicFiles(storagePath string) ([]string, error) {
	info, err := os.Stat(storagePath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", storagePath, err)
	}

	// If it's a file, check if it's a comic
	if !info.IsDir() {
		if archive.IsComicFile(storagePath) {
			return []string{storagePath}, nil
		}
		return nil, nil
	}

	// Walk the directory
	var files []string
	err = filepath.Walk(storagePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if !info.IsDir() && archive.IsComicFile(path) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return files, nil
}

// moveFile moves src to dst, falling back to copy+delete if rename fails
// (e.g. cross-device move).
func moveFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}

	// Rename failed — likely cross-device. Copy and delete.
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("creating destination: %w", err)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		os.Remove(dst) // clean up partial copy
		return fmt.Errorf("copying: %w", err)
	}

	if err := dstFile.Close(); err != nil {
		os.Remove(dst)
		return fmt.Errorf("closing destination: %w", err)
	}

	srcFile.Close()
	if err := os.Remove(src); err != nil {
		slog.Warn("failed to remove source after copy", "src", src, "error", err)
	}

	return nil
}

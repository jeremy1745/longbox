package service

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jeremy/longbox/internal/repository"
	tmpl "github.com/jeremy/longbox/internal/template"
)

const settingKeyNamingTemplate = "naming_template"

// RenamePreview describes a planned file rename.
type RenamePreview struct {
	FileID      int64  `json:"file_id"`
	CurrentPath string `json:"current_path"`
	NewPath     string `json:"new_path"`
	Status      string `json:"status"` // "move", "skip", "conflict", "unlinked"
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
	fileRepo    *repository.FileRepo
	issueRepo   *repository.IssueRepo
	seriesRepo  *repository.SeriesRepo
	settingRepo *repository.SettingRepo
}

func NewFileOrganizerService(
	fileRepo *repository.FileRepo,
	issueRepo *repository.IssueRepo,
	seriesRepo *repository.SeriesRepo,
	settingRepo *repository.SettingRepo,
) *FileOrganizerService {
	return &FileOrganizerService{
		fileRepo:    fileRepo,
		issueRepo:   issueRepo,
		seriesRepo:  seriesRepo,
		settingRepo: settingRepo,
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

	var previews []RenamePreview
	newPaths := make(map[string]int64) // track path → fileID to detect conflicts

	for _, f := range files {
		preview := RenamePreview{
			FileID:      f.ID,
			CurrentPath: f.FilePath,
		}

		// Skip files without a linked issue
		if f.IssueID == nil {
			preview.Status = "unlinked"
			preview.Reason = "File not linked to an issue"
			preview.NewPath = f.FilePath
			previews = append(previews, preview)
			continue
		}

		// Load issue and series data
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

		// Build template context
		ctx := tmpl.TemplateContext{
			Series:     series.Title,
			SortSeries: series.SortTitle,
			Number:     issue.IssueNumber,
			Title:      issue.Title,
			Format:     f.FileFormat,
			CoverDate:  issue.CoverDate,
			StoreDate:  issue.StoreDate,
			Publisher:  series.PublisherName,
		}
		if issue.Writers != "" {
			// Use first writer only for filename
			ctx.Writers = strings.SplitN(issue.Writers, ",", 2)[0]
			ctx.Writers = strings.TrimSpace(ctx.Writers)
		}
		if issue.Artists != "" {
			ctx.Artists = strings.SplitN(issue.Artists, ",", 2)[0]
			ctx.Artists = strings.TrimSpace(ctx.Artists)
		}

		// Execute template
		relPath, err := t.Execute(ctx)
		if err != nil {
			preview.Status = "unlinked"
			preview.Reason = fmt.Sprintf("Template error: %v", err)
			preview.NewPath = f.FilePath
			previews = append(previews, preview)
			continue
		}

		newFullPath := filepath.Join(libraryDir, relPath)
		preview.NewPath = newFullPath

		// Check if already at correct location
		if filepath.Clean(f.FilePath) == filepath.Clean(newFullPath) {
			preview.Status = "skip"
			preview.Reason = "Already organized"
			previews = append(previews, preview)
			continue
		}

		// Check for conflicts
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

// Execute actually moves files according to the current template.
func (s *FileOrganizerService) Execute(libraryDir string) (*RenameResult, error) {
	previews, err := s.Preview(libraryDir, "")
	if err != nil {
		return nil, err
	}

	result := &RenameResult{
		TotalFiles: len(previews),
	}

	// Track original directories for cleanup
	origDirs := make(map[string]bool)

	for _, p := range previews {
		if p.Status != "move" {
			result.Skipped++
			continue
		}

		// Track original directory for later cleanup
		origDirs[filepath.Dir(p.CurrentPath)] = true

		// Ensure target directory exists
		targetDir := filepath.Dir(p.NewPath)
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			result.Errors++
			result.ErrorDetails = append(result.ErrorDetails,
				fmt.Sprintf("Failed to create dir %s: %v", targetDir, err))
			continue
		}

		// Check if target already exists (safety check)
		if _, err := os.Stat(p.NewPath); err == nil {
			result.Errors++
			result.ErrorDetails = append(result.ErrorDetails,
				fmt.Sprintf("Target already exists: %s", p.NewPath))
			continue
		}

		// Move the file
		if err := os.Rename(p.CurrentPath, p.NewPath); err != nil {
			result.Errors++
			result.ErrorDetails = append(result.ErrorDetails,
				fmt.Sprintf("Failed to move %s → %s: %v", filepath.Base(p.CurrentPath), filepath.Base(p.NewPath), err))
			continue
		}

		// Update database
		newName := filepath.Base(p.NewPath)
		if err := s.fileRepo.UpdatePath(p.FileID, p.NewPath, newName); err != nil {
			// File was moved but DB update failed — try to move it back
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

	// Clean up empty directories
	for dir := range origDirs {
		cleanEmptyDirs(dir, libraryDir)
	}

	slog.Info("organize complete",
		"moved", result.Moved,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)

	return result, nil
}

// cleanEmptyDirs removes empty directories from bottom up, stopping at libraryDir.
func cleanEmptyDirs(dir, stopAt string) {
	stopAt = filepath.Clean(stopAt)
	for {
		dir = filepath.Clean(dir)
		if dir == stopAt || !strings.HasPrefix(dir, stopAt) {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

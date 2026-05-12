package service

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	tmpl "github.com/jeremy/longbox/internal/template"
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

// Execute actually moves files according to the current template.
func (s *FileOrganizerService) Execute(libraryDir string) (*RenameResult, error) {
	previews, err := s.Preview(libraryDir, "")
	if err != nil {
		return nil, err
	}

	result := &RenameResult{TotalFiles: len(previews)}
	origDirs := make(map[string]bool)

	for _, p := range previews {
		if p.Status != "move" {
			result.Skipped++
			continue
		}

		origDirs[filepath.Dir(p.CurrentPath)] = true

		targetDir := filepath.Dir(p.NewPath)
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			result.Errors++
			result.ErrorDetails = append(result.ErrorDetails,
				fmt.Sprintf("Failed to create dir %s: %v", targetDir, err))
			continue
		}

		if _, err := os.Stat(p.NewPath); err == nil {
			result.Errors++
			result.ErrorDetails = append(result.ErrorDetails,
				fmt.Sprintf("Target already exists: %s", p.NewPath))
			continue
		}

		if err := os.Rename(p.CurrentPath, p.NewPath); err != nil {
			result.Errors++
			result.ErrorDetails = append(result.ErrorDetails,
				fmt.Sprintf("Failed to move %s → %s: %v", filepath.Base(p.CurrentPath), filepath.Base(p.NewPath), err))
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

	slog.Info("organize complete",
		"moved", result.Moved,
		"skipped", result.Skipped,
		"errors", result.Errors,
	)

	return result, nil
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
	if series.Year != nil {
		ctx.SeriesYear = fmt.Sprintf("%d", *series.Year)
	}
	if year := extractYear(issue.CoverDate, issue.StoreDate); year != "" {
		ctx.Year = year
	} else if series.Year != nil {
		ctx.Year = fmt.Sprintf("%d", *series.Year)
	}

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
		parentName := tmpl.SanitizePathComponent(parentSeries.Title)
		relPath = filepath.Join(parentName, s.annualFolder, relPath)
	}

	return relPath, nil
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

// cleanEmptyDirs removes empty directories from bottom up, stopping at
// libraryDir.
//
// Containment is checked by appending a path separator to stopAt before the
// prefix test. Without the separator, "E:\Comics" would prefix-match
// "E:\Comics2\..." and the walk-up could escape the library and start
// deleting siblings.
func cleanEmptyDirs(dir, stopAt string) {
	stopAt = filepath.Clean(stopAt)
	stopAtWithSep := stopAt + string(filepath.Separator)
	for {
		dir = filepath.Clean(dir)
		if dir == stopAt || !strings.HasPrefix(dir, stopAtWithSep) {
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

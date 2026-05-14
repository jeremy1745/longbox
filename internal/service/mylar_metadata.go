package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/jeremy/longbox/internal/comicvine"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
)

const (
	longboxSeriesJSONFilename = "longbox-series.json"
	longboxSeriesTextFilename = "longbox-series.txt"
	longboxSidecarSchema      = "longbox.series.v1"
)

// LongboxMetadataService writes LongBox-native series sidecars to series folders.
//
// Sidecars are intentionally explicit and LongBox-specific:
//   - longbox-series.json: stable machine-friendly metadata for automation/tools.
//   - longbox-series.txt: human-readable summary for quick inspection.
//
// Unlike the old Mylar-compatible sidecars, this format does not write poster images
// or compatibility files such as cvinfo/poster.jpg. Cover URLs remain embedded in the
// JSON so downstream tools can fetch images if they actually want them.
type LongboxMetadataService struct {
	seriesRepo *repository.SeriesRepo
	fileRepo   *repository.FileRepo
	issueRepo  *repository.IssueRepo
	cvClient   *comicvine.Client
	librarySvc *LibraryService

	locksMu  sync.Mutex
	dirLocks map[string]*sync.Mutex
}

// NewLongboxMetadataService creates a new service for writing LongBox-native metadata sidecars.
func NewLongboxMetadataService(
	seriesRepo *repository.SeriesRepo,
	fileRepo *repository.FileRepo,
	issueRepo *repository.IssueRepo,
	cvClient *comicvine.Client,
	librarySvc *LibraryService,
) *LongboxMetadataService {
	return &LongboxMetadataService{
		seriesRepo: seriesRepo,
		fileRepo:   fileRepo,
		issueRepo:  issueRepo,
		cvClient:   cvClient,
		librarySvc: librarySvc,
		dirLocks:   make(map[string]*sync.Mutex),
	}
}

// SidecarWriteOutcome captures the result of writing one series' sidecars.
type SidecarWriteOutcome int

const (
	SidecarWritten SidecarWriteOutcome = iota
	SidecarSkippedUnchanged
	SidecarSkippedNoFiles
	SidecarSkippedNoCVMatch
	SidecarSkippedNoFolder
	SidecarFailed
)

// WriteAll writes LongBox-native sidecars for all series matched to ComicVine that have files on disk.
//
// Issues and files are prefetched into per-series maps in two queries up
// front instead of looking them up per series. On a 5k-series library this
// turns ~10k DB round-trips (3 per series) into 2 — and on a network DB
// (rare for SQLite but the cost is real for large in-memory traversals)
// it turns minutes into seconds.
func (s *LongboxMetadataService) WriteAll(
	ctx context.Context,
	progress func(processed, total int, message string),
) (written, skipped, failed int, err error) {
	if progress == nil {
		progress = func(int, int, string) {}
	}
	seriesList, err := s.seriesRepo.ListWithComicVineID()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("listing series: %w", err)
	}

	progress(0, len(seriesList), "Loading issues + files (bulk prefetch)")
	filesBySeries, err := s.fileRepo.ListAllGroupedBySeries()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("prefetching files: %w", err)
	}
	issuesBySeries, err := s.issueRepo.ListAllGroupedBySeries()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("prefetching issues: %w", err)
	}

	libraryDir := s.libraryDirOrEmpty()
	total := len(seriesList)
	for i, series := range seriesList {
		select {
		case <-ctx.Done():
			return written, skipped, failed, ctx.Err()
		default:
		}

		progress(i, total, fmt.Sprintf("Writing LongBox sidecars for %s (%d/%d)", series.Title, i+1, total))

		switch s.writeOneWithPrefetch(&series, libraryDir, filesBySeries[series.ID], issuesBySeries[series.ID]) {
		case SidecarWritten:
			written++
		case SidecarSkippedUnchanged, SidecarSkippedNoFiles, SidecarSkippedNoCVMatch, SidecarSkippedNoFolder:
			skipped++
		case SidecarFailed:
			failed++
		}
	}

	progress(total, total, fmt.Sprintf("Done: %d updated, %d unchanged, %d failed", written, skipped, failed))
	return written, skipped, failed, nil
}

// WriteForSeries writes the LongBox sidecars for a single series. Returns the
// outcome and the on-disk seriesDir that was used (empty if no folder could
// be determined).
func (s *LongboxMetadataService) WriteForSeries(seriesID int64) (SidecarWriteOutcome, string, error) {
	series, err := s.seriesRepo.GetByID(seriesID)
	if err != nil {
		return SidecarFailed, "", fmt.Errorf("looking up series: %w", err)
	}
	if series == nil {
		return SidecarFailed, "", fmt.Errorf("series %d not found", seriesID)
	}
	libraryDir := s.libraryDirOrEmpty()
	outcome := s.writeOne(series, libraryDir)
	dir := s.lastSeriesDir(series, libraryDir)
	return outcome, dir, nil
}

func (s *LongboxMetadataService) libraryDirOrEmpty() string {
	if s.librarySvc == nil {
		return ""
	}
	return s.librarySvc.GetLibraryDir()
}

// lastSeriesDir recomputes the directory that writeOne would have used. Only
// for surfacing the path back to API callers — does not write anything.
func (s *LongboxMetadataService) lastSeriesDir(series *model.Series, libraryDir string) string {
	files, err := s.fileRepo.ListBySeries(series.ID)
	if err != nil || len(files) == 0 {
		return ""
	}
	return determineSeriesFolder(files, libraryDir)
}

// writeOne is the per-series entry point used by single-series API calls.
// Loads files + issues on demand and delegates to writeOneWithPrefetch.
func (s *LongboxMetadataService) writeOne(series *model.Series, libraryDir string) SidecarWriteOutcome {
	if series.ComicVineID == nil && series.MetronID == nil {
		return SidecarSkippedNoCVMatch
	}
	files, err := s.fileRepo.ListBySeries(series.ID)
	if err != nil {
		slog.Warn("failed to list files for series", "series_id", series.ID, "title", series.Title, "error", err)
		return SidecarFailed
	}
	if len(files) == 0 {
		slog.Debug("no files for series, skipping longbox sidecars", "series_id", series.ID, "title", series.Title)
		return SidecarSkippedNoFiles
	}
	issues, err := s.issueRepo.ListBySeries(series.ID)
	if err != nil {
		slog.Warn("failed to list issues for series",
			"series_id", series.ID, "title", series.Title, "error", err)
		return SidecarFailed
	}
	return s.writeOneWithPrefetch(series, libraryDir, files, issues)
}

// writeOneWithPrefetch is the bulk-job entry point — files + issues are
// already loaded by WriteAll's two-query prefetch. Uses local DB fields
// exclusively — no upstream provider call — so the sidecar job never
// burns ComicVine / Metron quota and isn't blocked by exhausted limits.
func (s *LongboxMetadataService) writeOneWithPrefetch(
	series *model.Series,
	libraryDir string,
	files []model.ComicFile,
	issues []model.Issue,
) SidecarWriteOutcome {
	if series.ComicVineID == nil && series.MetronID == nil {
		return SidecarSkippedNoCVMatch
	}
	if len(files) == 0 {
		return SidecarSkippedNoFiles
	}

	seriesDir := determineSeriesFolder(files, libraryDir)
	if seriesDir == "" {
		slog.Warn("could not determine series folder (would have landed at or above libraryDir)",
			"series_id", series.ID, "title", series.Title, "library_dir", libraryDir)
		return SidecarSkippedNoFolder
	}

	sidecar := buildLongboxSeriesSidecar(series, issues, files, seriesDir)
	jsonData, err := json.MarshalIndent(sidecar, "", "  ")
	if err != nil {
		slog.Warn("failed to marshal longbox sidecar json", "series_id", series.ID, "title", series.Title, "error", err)
		return SidecarFailed
	}
	jsonData = append(jsonData, '\n')
	textData := []byte(renderLongboxSeriesSummary(sidecar))

	unlock := s.lockSeriesDir(seriesDir)
	jsonChanged, jsonErr := writeFileAtomicIfChanged(filepath.Join(seriesDir, longboxSeriesJSONFilename), jsonData, 0644)
	textChanged, textErr := writeFileAtomicIfChanged(filepath.Join(seriesDir, longboxSeriesTextFilename), textData, 0644)
	unlock()

	if jsonErr != nil || textErr != nil {
		if jsonErr != nil {
			slog.Warn("failed to write longbox json sidecar", "series_id", series.ID, "title", series.Title, "error", jsonErr)
		}
		if textErr != nil {
			slog.Warn("failed to write longbox text sidecar", "series_id", series.ID, "title", series.Title, "error", textErr)
		}
		return SidecarFailed
	}

	if jsonChanged || textChanged {
		slog.Info("wrote longbox sidecars",
			"series_id", series.ID,
			"series", series.Title,
			"folder", seriesDir,
			"json_changed", jsonChanged,
			"text_changed", textChanged,
		)
		return SidecarWritten
	}

	slog.Debug("longbox sidecars unchanged",
		"series_id", series.ID,
		"series", series.Title,
		"folder", seriesDir,
	)
	return SidecarSkippedUnchanged
}

type longboxSeriesSidecar struct {
	Schema       string                        `json:"schema"`
	Version      int                           `json:"version"`
	Series       longboxSeriesRecord           `json:"series"`
	Stats        longboxSeriesStats            `json:"stats"`
	Files        longboxSeriesFiles            `json:"files"`
	IssueNumbers longboxIssueNumberCollections `json:"issue_numbers"`
	Issues       []longboxIssueRecord          `json:"issues"`
}

type longboxSeriesRecord struct {
	ID                 int64   `json:"id"`
	Title              string  `json:"title"`
	SortTitle          string  `json:"sort_title,omitempty"`
	Year               *int    `json:"year,omitempty"`
	Publisher          string  `json:"publisher,omitempty"`
	Status             string  `json:"status,omitempty"`
	Tracked            bool    `json:"tracked"`
	MetadataLocked     bool    `json:"metadata_locked"`
	ComicVineID        int64   `json:"comicvine_id"`
	ComicVineURL       string  `json:"comicvine_url"`
	Description        string  `json:"description,omitempty"`
	CoverImageURL      string  `json:"cover_image_url,omitempty"`
	IssueCountExpected int     `json:"issue_count_expected"`
	Folder             string  `json:"folder"`
	ParentSeriesID     *int64  `json:"parent_series_id,omitempty"`
	LastCVSync         *string `json:"last_cv_sync,omitempty"`
}

type longboxSeriesStats struct {
	IssueCount         int `json:"issue_count"`
	OwnedCount         int `json:"owned_count"`
	MissingCount       int `json:"missing_count"`
	ReadCount          int `json:"read_count"`
	SkippedCount       int `json:"skipped_count"`
	WithComicInfoCount int `json:"with_comicinfo_count"`
}

type longboxSeriesFiles struct {
	Count int   `json:"count"`
	Bytes int64 `json:"bytes"`
}

type longboxIssueNumberCollections struct {
	Owned   []string `json:"owned"`
	Missing []string `json:"missing"`
	Read    []string `json:"read"`
	Skipped []string `json:"skipped"`
}

type longboxIssueRecord struct {
	ID             int64  `json:"id"`
	IssueNumber    string `json:"issue_number"`
	Title          string `json:"title,omitempty"`
	SortNumber     string `json:"sort_number"`
	ComicVineID    *int64 `json:"comicvine_id,omitempty"`
	ComicVineURL   string `json:"comicvine_url,omitempty"`
	CoverDate      string `json:"cover_date,omitempty"`
	StoreDate      string `json:"store_date,omitempty"`
	HasFile        bool   `json:"has_file"`
	FileID         *int64 `json:"file_id,omitempty"`
	ReadStatus     string `json:"read_status,omitempty"`
	SkipStatus     string `json:"skip_status,omitempty"`
	MetadataLocked bool   `json:"metadata_locked"`
	Writers        string `json:"writers,omitempty"`
	Artists        string `json:"artists,omitempty"`
}

func buildLongboxSeriesSidecar(series *model.Series, issues []model.Issue, files []model.ComicFile, seriesDir string) longboxSeriesSidecar {
	publisher := series.PublisherName

	comicVineURL := ""
	if series.ComicVineID != nil {
		comicVineURL = fmt.Sprintf("https://comicvine.gamespot.com/volume/4050-%d/", *series.ComicVineID)
	}

	var cvID int64
	if series.ComicVineID != nil {
		cvID = *series.ComicVineID
	}

	sidecar := longboxSeriesSidecar{
		Schema:  longboxSidecarSchema,
		Version: 1,
		Series: longboxSeriesRecord{
			ID:                 series.ID,
			Title:              series.Title,
			SortTitle:          series.SortTitle,
			Year:               series.Year,
			Publisher:          publisher,
			Status:             series.Status,
			Tracked:            series.Tracked,
			MetadataLocked:     series.MetadataLocked,
			ComicVineID:        cvID,
			ComicVineURL:       comicVineURL,
			Description:        strings.TrimSpace(series.Description),
			CoverImageURL:      series.CoverImageURL,
			IssueCountExpected: series.TotalIssues,
			Folder:             seriesDir,
			ParentSeriesID:     series.ParentSeriesID,
			LastCVSync:         series.LastCVSync,
		},
		Issues: make([]longboxIssueRecord, 0, len(issues)),
	}

	for _, file := range files {
		sidecar.Files.Count++
		sidecar.Files.Bytes += file.FileSize
		if file.HasComicInfo {
			sidecar.Stats.WithComicInfoCount++
		}
	}

	for _, issue := range issues {
		record := longboxIssueRecord{
			ID:             issue.ID,
			IssueNumber:    issue.IssueNumber,
			Title:          issue.Title,
			SortNumber:     formatSortNumber(issue.SortNumber),
			ComicVineID:    issue.ComicVineID,
			CoverDate:      issue.CoverDate,
			StoreDate:      issue.StoreDate,
			HasFile:        issue.HasFile,
			FileID:         issue.FileID,
			ReadStatus:     issue.ReadStatus,
			MetadataLocked: issue.MetadataLocked,
			Writers:        issue.Writers,
			Artists:        issue.Artists,
		}
		if issue.ComicVineID != nil {
			record.ComicVineURL = fmt.Sprintf("https://comicvine.gamespot.com/issue/4000-%d/", *issue.ComicVineID)
		}
		if issue.SkipStatus != nil {
			record.SkipStatus = *issue.SkipStatus
		}

		sidecar.Stats.IssueCount++
		if issue.HasFile {
			sidecar.Stats.OwnedCount++
			sidecar.IssueNumbers.Owned = append(sidecar.IssueNumbers.Owned, issue.IssueNumber)
		} else {
			sidecar.Stats.MissingCount++
			sidecar.IssueNumbers.Missing = append(sidecar.IssueNumbers.Missing, issue.IssueNumber)
		}
		if issue.ReadStatus == "read" {
			sidecar.Stats.ReadCount++
			sidecar.IssueNumbers.Read = append(sidecar.IssueNumbers.Read, issue.IssueNumber)
		}
		if issue.SkipStatus != nil && *issue.SkipStatus != "" {
			sidecar.Stats.SkippedCount++
			sidecar.IssueNumbers.Skipped = append(sidecar.IssueNumbers.Skipped, issue.IssueNumber)
		}

		sidecar.Issues = append(sidecar.Issues, record)
	}

	return sidecar
}

func renderLongboxSeriesSummary(sidecar longboxSeriesSidecar) string {
	var b strings.Builder

	line := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteByte('\n')
	}

	line("LongBox Series Summary")
	line("======================")
	line("")
	line("Title: %s", sidecar.Series.Title)
	if sidecar.Series.Year != nil {
		line("Year: %d", *sidecar.Series.Year)
	}
	if sidecar.Series.Publisher != "" {
		line("Publisher: %s", sidecar.Series.Publisher)
	}
	line("Status: %s", fallbackText(sidecar.Series.Status, "unknown"))
	line("Tracked: %s", yesNo(sidecar.Series.Tracked))
	line("Metadata Locked: %s", yesNo(sidecar.Series.MetadataLocked))
	line("ComicVine: %s", sidecar.Series.ComicVineURL)
	line("Folder: %s", sidecar.Series.Folder)
	line("")
	line("Counts")
	line("------")
	line("Issues in database: %d", sidecar.Stats.IssueCount)
	line("Expected from ComicVine: %d", sidecar.Series.IssueCountExpected)
	line("Owned: %d", sidecar.Stats.OwnedCount)
	line("Missing: %d", sidecar.Stats.MissingCount)
	line("Read: %d", sidecar.Stats.ReadCount)
	line("Skipped: %d", sidecar.Stats.SkippedCount)
	line("Files: %d", sidecar.Files.Count)
	line("Files with ComicInfo.xml: %d", sidecar.Stats.WithComicInfoCount)
	line("")

	if sidecar.Series.Description != "" {
		line("Description")
		line("-----------")
		for _, part := range wrapText(sidecar.Series.Description, 100) {
			line("%s", part)
		}
		line("")
	}

	if len(sidecar.IssueNumbers.Owned) > 0 {
		line("Owned Issues: %s", strings.Join(sidecar.IssueNumbers.Owned, ", "))
	}
	if len(sidecar.IssueNumbers.Missing) > 0 {
		line("Missing Issues: %s", strings.Join(sidecar.IssueNumbers.Missing, ", "))
	}
	if len(sidecar.IssueNumbers.Read) > 0 {
		line("Read Issues: %s", strings.Join(sidecar.IssueNumbers.Read, ", "))
	}
	if len(sidecar.IssueNumbers.Skipped) > 0 {
		line("Skipped Issues: %s", strings.Join(sidecar.IssueNumbers.Skipped, ", "))
	}
	if len(sidecar.IssueNumbers.Owned) > 0 || len(sidecar.IssueNumbers.Missing) > 0 || len(sidecar.IssueNumbers.Read) > 0 || len(sidecar.IssueNumbers.Skipped) > 0 {
		line("")
	}

	line("Issue Details")
	line("-------------")
	for _, issue := range sidecar.Issues {
		status := "missing"
		if issue.HasFile {
			status = "owned"
		}
		if issue.SkipStatus != "" {
			status += ", skipped"
		}
		if issue.ReadStatus == "read" {
			status += ", read"
		}

		title := issue.Title
		if title == "" {
			title = "(untitled)"
		}
		line("#%s — %s [%s]", issue.IssueNumber, title, status)
		if issue.StoreDate != "" || issue.CoverDate != "" {
			line("  cover: %s | store: %s", fallbackText(issue.CoverDate, "-"), fallbackText(issue.StoreDate, "-"))
		}
	}

	return strings.TrimRight(b.String(), "\n") + "\n"
}

func wrapText(text string, maxWidth int) []string {
	if maxWidth <= 0 || len(text) <= maxWidth {
		return []string{text}
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}

	lines := make([]string, 0, len(words)/8+1)
	current := words[0]
	for _, word := range words[1:] {
		if len(current)+1+len(word) > maxWidth {
			lines = append(lines, current)
			current = word
			continue
		}
		current += " " + word
	}
	lines = append(lines, current)
	return lines
}

func formatSortNumber(v float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", v), "0"), ".")
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func fallbackText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (s *LongboxMetadataService) lockSeriesDir(seriesDir string) func() {
	s.locksMu.Lock()
	mu, ok := s.dirLocks[seriesDir]
	if !ok {
		mu = &sync.Mutex{}
		s.dirLocks[seriesDir] = mu
	}
	s.locksMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

// determineSeriesFolder returns the best series directory for the given files.
//
// Prefer the deepest common ancestor so sidecars land in the actual series root
// even when files are split across child folders (for example Annuals/, Specials/,
// or per-issue subfolders). If there is no meaningful common ancestor, fall back to
// the most common existing parent directory. If libraryDir is provided and
// the computed common ancestor lands AT or ABOVE it (e.g., when a series'
// files are split across two top-level folders), the function falls back to
// the most-populous subdirectory under libraryDir to avoid writing the
// sidecar to the library root and overwriting another series' file.
func determineSeriesFolder(files []model.ComicFile, libraryDir string) string {
	if len(files) == 0 {
		return ""
	}

	candidate := filepath.Clean(filepath.Dir(files[0].FilePath))
	for _, f := range files[1:] {
		candidate = commonAncestorDir(candidate, filepath.Dir(f.FilePath))
		if candidate == "" {
			break
		}
	}
	if candidate != "" && candidate != "." && !isLibraryRootOrAbove(candidate, libraryDir) {
		return candidate
	}

	fallback := mostCommonParentDir(files)
	if isLibraryRootOrAbove(fallback, libraryDir) {
		return ""
	}
	return fallback
}

// isLibraryRootOrAbove reports whether path equals libraryDir or is an ancestor
// of it. If libraryDir is empty, no constraint applies.
func isLibraryRootOrAbove(path, libraryDir string) bool {
	if libraryDir == "" || path == "" {
		return false
	}
	p := filepath.Clean(path)
	root := filepath.Clean(libraryDir)
	if samePath(p, root) {
		return true
	}
	return hasPathPrefix(root, p+string(os.PathSeparator))
}

func commonAncestorDir(a, b string) string {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if a == "" || b == "" {
		return ""
	}

	sep := string(os.PathSeparator)
	for {
		if samePath(a, b) || hasPathPrefix(b, a+sep) {
			return a
		}

		parent := filepath.Dir(a)
		if parent == a || parent == "." || parent == "" {
			return ""
		}
		a = parent
	}
}

// hasPathPrefix is samePath-aware HasPrefix — case-insensitive on Windows,
// exact otherwise.
func hasPathPrefix(s, prefix string) bool {
	if os.PathSeparator == '\\' {
		return len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix)
	}
	return strings.HasPrefix(s, prefix)
}

func samePath(a, b string) bool {
	if a == b {
		return true
	}
	if os.PathSeparator == '\\' {
		return strings.EqualFold(a, b)
	}
	return false
}

func mostCommonParentDir(files []model.ComicFile) string {
	dirs := make(map[string]int)
	for _, f := range files {
		dir := filepath.Clean(filepath.Dir(f.FilePath))
		dirs[dir]++
	}

	keys := make([]string, 0, len(dirs))
	for d := range dirs {
		keys = append(keys, d)
	}
	// Deterministic tie-breaker: prefer deeper (more separators), then lexical.
	sort.Slice(keys, func(i, j int) bool {
		ci, cj := dirs[keys[i]], dirs[keys[j]]
		if ci != cj {
			return ci > cj
		}
		di := strings.Count(keys[i], string(os.PathSeparator))
		dj := strings.Count(keys[j], string(os.PathSeparator))
		if di != dj {
			return di > dj
		}
		return keys[i] < keys[j]
	})
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// bestImageURL returns the best available image URL from a ComicVine Image object.
func bestImageURL(img *comicvine.Image) string {
	if img == nil {
		return ""
	}
	if img.SuperURL != "" {
		return img.SuperURL
	}
	if img.OriginalURL != "" {
		return img.OriginalURL
	}
	if img.ScreenLargeURL != "" {
		return img.ScreenLargeURL
	}
	if img.MediumURL != "" {
		return img.MediumURL
	}
	return ""
}

func writeFileAtomicIfChanged(destPath string, data []byte, perm os.FileMode) (bool, error) {
	if existing, err := os.ReadFile(destPath); err == nil && bytes.Equal(existing, data) {
		return false, nil
	} else if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("reading existing file: %w", err)
	}

	dir := filepath.Dir(destPath)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(destPath)+"-*.tmp")
	if err != nil {
		return false, fmt.Errorf("creating temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return false, fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmpFile.Chmod(perm); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return false, fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return false, fmt.Errorf("closing temp file: %w", err)
	}

	changed, err := replaceFileIfChanged(tmpPath, destPath)
	if err != nil {
		return false, err
	}

	return changed, nil
}

func replaceFileIfChanged(tmpPath, destPath string) (bool, error) {
	tmpData, err := os.ReadFile(tmpPath)
	if err != nil {
		os.Remove(tmpPath)
		return false, fmt.Errorf("reading temp file: %w", err)
	}

	if existing, err := os.ReadFile(destPath); err == nil && bytes.Equal(existing, tmpData) {
		os.Remove(tmpPath)
		return false, nil
	} else if err != nil && !os.IsNotExist(err) {
		os.Remove(tmpPath)
		return false, fmt.Errorf("reading existing file: %w", err)
	}

	// Windows does not allow os.Rename to replace an existing file in place.
	// Remove the destination first after confirming the content changed.
	if _, err := os.Stat(destPath); err == nil {
		if err := os.Remove(destPath); err != nil {
			os.Remove(tmpPath)
			return false, fmt.Errorf("removing existing destination file: %w", err)
		}
	} else if !os.IsNotExist(err) {
		os.Remove(tmpPath)
		return false, fmt.Errorf("stating existing destination file: %w", err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		os.Remove(tmpPath)
		return false, fmt.Errorf("renaming temp file: %w", err)
	}

	return true, nil
}

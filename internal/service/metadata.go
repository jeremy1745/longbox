package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jeremy/longbox/internal/comicvine"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scanner"
	"github.com/jeremy/longbox/internal/walksoftly"
)

// MetadataService handles ComicVine metadata operations.
type MetadataService struct {
	cv               *comicvine.Client
	ws               *walksoftly.Client
	seriesRepo       *repository.SeriesRepo
	issueRepo        *repository.IssueRepo
	publisherRepo    *repository.PublisherRepo
	wantListRepo     *repository.WantListRepo
	settingRepo      *repository.SettingRepo
	configAPIKey     string
	configLibraryDir string

	// In-memory cache: ComicVine volume ID → publisher name.
	// Avoids re-fetching publisher data on every pull list page load.
	publisherCache map[int]string
}

func NewMetadataService(
	cv *comicvine.Client,
	ws *walksoftly.Client,
	seriesRepo *repository.SeriesRepo,
	issueRepo *repository.IssueRepo,
	publisherRepo *repository.PublisherRepo,
	wantListRepo *repository.WantListRepo,
	settingRepo *repository.SettingRepo,
	configAPIKey string,
	configLibraryDir string,
) *MetadataService {
	return &MetadataService{
		cv:               cv,
		ws:               ws,
		seriesRepo:       seriesRepo,
		issueRepo:        issueRepo,
		publisherRepo:    publisherRepo,
		wantListRepo:     wantListRepo,
		settingRepo:      settingRepo,
		configAPIKey:     configAPIKey,
		configLibraryDir: configLibraryDir,
		publisherCache:   make(map[int]string),
	}
}

// EnsureAPIKey loads the API key from settings (priority) or config.
func (s *MetadataService) EnsureAPIKey() error {
	// Settings DB takes priority over config file
	dbKey, err := s.settingRepo.Get("comicvine_api_key")
	if err != nil {
		return fmt.Errorf("reading api key from settings: %w", err)
	}
	if dbKey != "" {
		s.cv.SetAPIKey(dbKey)
		return nil
	}
	if s.configAPIKey != "" {
		s.cv.SetAPIKey(s.configAPIKey)
		return nil
	}
	return nil
}

// HasAPIKey returns true if a ComicVine API key is available.
func (s *MetadataService) HasAPIKey() bool {
	return s.cv.HasAPIKey()
}

// GetAPIKey returns the current API key (masked for display).
func (s *MetadataService) GetAPIKeyMasked() string {
	dbKey, _ := s.settingRepo.Get("comicvine_api_key")
	key := dbKey
	if key == "" {
		key = s.configAPIKey
	}
	if key == "" {
		return ""
	}
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-4:]
}

// GetAPIKeySource returns where the API key is coming from.
func (s *MetadataService) GetAPIKeySource() string {
	dbKey, _ := s.settingRepo.Get("comicvine_api_key")
	if dbKey != "" {
		return "settings"
	}
	if s.configAPIKey != "" {
		return "config"
	}
	return "none"
}

// SetAPIKey saves the API key to the settings database.
func (s *MetadataService) SetAPIKey(key string) error {
	if err := s.settingRepo.Set("comicvine_api_key", key); err != nil {
		return err
	}
	s.cv.SetAPIKey(key)
	return nil
}

// HourlyRemaining returns how many API calls are left this hour.
func (s *MetadataService) HourlyRemaining() int {
	return s.cv.HourlyRemaining()
}

// SearchResult wraps ComicVine search results with match scoring.
type MetadataSearchResult struct {
	ComicVineID   int    `json:"comicvine_id"`
	Name          string `json:"name"`
	StartYear     string `json:"start_year"`
	IssueCount    int    `json:"issue_count"`
	Publisher     string `json:"publisher"`
	Description   string `json:"description"`
	ImageURL      string `json:"image_url"`
	ResourceType  string `json:"resource_type"`
	MatchScore    int    `json:"match_score"`
}

// SearchVolumes searches ComicVine for volumes matching a query.
func (s *MetadataService) SearchVolumes(query string, page int) ([]MetadataSearchResult, int, error) {
	if !s.cv.HasAPIKey() {
		return nil, 0, fmt.Errorf("ComicVine API key not configured")
	}

	results, total, err := s.cv.SearchVolumes(query, page)
	if err != nil {
		return nil, 0, err
	}

	var out []MetadataSearchResult
	for _, r := range results {
		publisher := ""
		if r.Publisher != nil {
			publisher = r.Publisher.Name
		}
		imageURL := ""
		if r.Image != nil {
			imageURL = r.Image.SmallURL
		}
		desc := comicvine.StripHTML(r.Description)
		if len(desc) > 300 {
			desc = desc[:300] + "..."
		}

		out = append(out, MetadataSearchResult{
			ComicVineID:  r.ID,
			Name:         r.Name,
			StartYear:    r.StartYear,
			IssueCount:   r.CountOfIssues,
			Publisher:    publisher,
			Description:  desc,
			ImageURL:     imageURL,
			ResourceType: r.ResourceType,
		})
	}

	return out, total, nil
}

// GetVolume fetches volume details from ComicVine.
func (s *MetadataService) GetVolume(cvID int) (*comicvine.Volume, error) {
	if !s.cv.HasAPIKey() {
		return nil, fmt.Errorf("ComicVine API key not configured")
	}
	return s.cv.GetVolume(cvID)
}

// MatchSeriesToVolume matches a local series to a ComicVine volume and applies metadata.
func (s *MetadataService) MatchSeriesToVolume(seriesID int64, cvVolumeID int) error {
	if !s.cv.HasAPIKey() {
		return fmt.Errorf("ComicVine API key not configured")
	}

	// Get local series
	series, err := s.seriesRepo.GetByID(seriesID)
	if err != nil {
		return fmt.Errorf("getting series: %w", err)
	}
	if series == nil {
		return fmt.Errorf("series %d not found", seriesID)
	}
	if series.MetadataLocked {
		return fmt.Errorf("series metadata is locked")
	}

	// Fetch volume from ComicVine
	volume, err := s.cv.GetVolume(cvVolumeID)
	if err != nil {
		return fmt.Errorf("fetching volume from ComicVine: %w", err)
	}

	// Handle publisher
	var publisherID *int64
	if volume.Publisher != nil && volume.Publisher.Name != "" {
		var cvPubID *int64
		if volume.Publisher.ID > 0 {
			id := int64(volume.Publisher.ID)
			cvPubID = &id
		}
		pub, err := s.publisherRepo.FindOrCreateByName(volume.Publisher.Name, cvPubID)
		if err != nil {
			slog.Warn("failed to create publisher", "name", volume.Publisher.Name, "error", err)
		} else if pub != nil {
			publisherID = &pub.ID
		}
	}

	// Update series metadata
	cvID := int64(volume.ID)
	series.ComicVineID = &cvID
	series.Title = volume.Name
	series.SortTitle = scanner.MakeSortTitle(volume.Name)
	series.PublisherID = publisherID
	series.Description = comicvine.StripHTML(volume.Description)
	series.TotalIssues = volume.CountOfIssues

	if volume.StartYear != "" {
		var year int
		fmt.Sscanf(volume.StartYear, "%d", &year)
		if year > 0 {
			series.Year = &year
		}
	}

	// Determine status
	series.Status = "continuing"

	if err := s.seriesRepo.UpdateFromMetadata(series); err != nil {
		return fmt.Errorf("updating series: %w", err)
	}

	slog.Info("matched series to ComicVine volume",
		"series_id", seriesID,
		"cv_volume_id", cvVolumeID,
		"title", volume.Name,
	)

	// Now fetch all issues for this volume and populate missing issues
	if err := s.populateIssuesFromVolume(series, volume); err != nil {
		slog.Warn("failed to populate issues from volume", "error", err)
	}

	return nil
}

// populateIssuesFromVolume creates issue records for any issues from the
// ComicVine volume that don't exist locally yet, and updates existing ones.
func (s *MetadataService) populateIssuesFromVolume(series *model.Series, volume *comicvine.Volume) error {
	// Get all issues from ComicVine for this volume
	cvIssues, err := s.cv.GetVolumeIssues(volume.ID)
	if err != nil {
		return fmt.Errorf("fetching volume issues: %w", err)
	}

	slog.Info("populating issues from ComicVine",
		"series_id", series.ID,
		"cv_issues", len(cvIssues),
	)

	created := 0
	updated := 0

	for _, cvIssue := range cvIssues {
		issueNumber := cvIssue.IssueNumber
		if issueNumber == "" {
			continue
		}

		// Build writers and artists strings
		var writers, artists []string
		for _, pc := range cvIssue.PersonCredits {
			role := strings.ToLower(pc.Role)
			if strings.Contains(role, "writer") {
				writers = append(writers, pc.Name)
			}
			if strings.Contains(role, "artist") || strings.Contains(role, "pencil") ||
				strings.Contains(role, "ink") || strings.Contains(role, "cover") {
				artists = append(artists, pc.Name)
			}
		}

		coverURL := ""
		if cvIssue.Image != nil {
			coverURL = cvIssue.Image.SmallURL
		}

		// Check if issue already exists locally
		existing, err := s.issueRepo.FindBySeriesAndNumber(series.ID, issueNumber)
		if err != nil {
			slog.Warn("error finding issue", "number", issueNumber, "error", err)
			continue
		}

		cvID := int64(cvIssue.ID)

		if existing != nil {
			// Update existing issue with metadata if not locked
			if !existing.MetadataLocked {
				existing.ComicVineID = &cvID
				existing.Title = cvIssue.Name
				existing.Description = comicvine.StripHTML(cvIssue.Description)
				existing.CoverDate = cvIssue.CoverDate
				existing.StoreDate = cvIssue.StoreDate
				existing.CoverURL = coverURL
				if len(writers) > 0 {
					existing.Writers = strings.Join(writers, ", ")
				}
				if len(artists) > 0 {
					existing.Artists = strings.Join(artists, ", ")
				}
				if err := s.issueRepo.UpdateFromMetadata(existing); err != nil {
					slog.Warn("failed to update issue", "id", existing.ID, "error", err)
				} else {
					updated++
				}
			}
		} else {
			// Create new issue record (missing issue)
			issue := &model.Issue{
				SeriesID:    series.ID,
				IssueNumber: issueNumber,
				SortNumber:  scanner.SortNumber(issueNumber),
				Title:       cvIssue.Name,
				ComicVineID: &cvID,
				Description: comicvine.StripHTML(cvIssue.Description),
				CoverDate:   cvIssue.CoverDate,
				StoreDate:   cvIssue.StoreDate,
				CoverURL:    coverURL,
				Writers:     strings.Join(writers, ", "),
				Artists:     strings.Join(artists, ", "),
				ReadStatus:  "unread",
			}
			if err := s.issueRepo.Create(issue); err != nil {
				slog.Warn("failed to create issue", "number", issueNumber, "error", err)
			} else {
				created++
			}
		}
	}

	slog.Info("issue population complete",
		"series_id", series.ID,
		"created", created,
		"updated", updated,
	)

	return nil
}

// RefreshSeriesMetadata re-fetches metadata for a series that's already matched.
func (s *MetadataService) RefreshSeriesMetadata(seriesID int64) error {
	series, err := s.seriesRepo.GetByID(seriesID)
	if err != nil {
		return fmt.Errorf("getting series: %w", err)
	}
	if series == nil {
		return fmt.Errorf("series %d not found", seriesID)
	}
	if series.ComicVineID == nil {
		return fmt.Errorf("series is not matched to ComicVine")
	}

	return s.MatchSeriesToVolume(seriesID, int(*series.ComicVineID))
}

// RefreshTrackedSeries re-fetches metadata from ComicVine for all tracked series.
// This picks up new issues (with store_dates) that have been added since last sync.
func (s *MetadataService) RefreshTrackedSeries(
	ctx context.Context,
	progress func(processed, total int, message string),
) (int, int, error) {
	if !s.cv.HasAPIKey() {
		return 0, 0, fmt.Errorf("ComicVine API key not configured")
	}

	tracked, err := s.seriesRepo.ListTracked()
	if err != nil {
		return 0, 0, fmt.Errorf("listing tracked series: %w", err)
	}

	total := len(tracked)
	refreshed := 0
	failed := 0

	for i, series := range tracked {
		select {
		case <-ctx.Done():
			return refreshed, failed, ctx.Err()
		default:
		}

		if progress != nil {
			progress(i, total, fmt.Sprintf("Refreshing %s", series.Title))
		}

		if series.ComicVineID == nil {
			slog.Debug("skipping unmatched tracked series", "id", series.ID, "title", series.Title)
			continue
		}

		if err := s.MatchSeriesToVolume(series.ID, int(*series.ComicVineID)); err != nil {
			slog.Warn("failed to refresh tracked series",
				"series_id", series.ID,
				"title", series.Title,
				"error", err,
			)
			failed++
			continue
		}
		refreshed++
	}

	if progress != nil {
		progress(total, total, fmt.Sprintf("Refreshed %d tracked series", refreshed))
	}

	slog.Info("tracked series refresh complete",
		"total", total,
		"refreshed", refreshed,
		"failed", failed,
	)

	return refreshed, failed, nil
}

// PullListIssue is a release-week issue combining ComicVine data with local ownership info.
type PullListIssue struct {
	// ComicVine data
	ComicVineID   int    `json:"comicvine_id,omitempty"`
	ComicVineURL  string `json:"comicvine_url,omitempty"`
	SeriesName    string `json:"series_name"`
	SeriesCVID    int    `json:"series_cv_id,omitempty"`
	IssueNumber   string `json:"issue_number"`
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
	StoreDate     string `json:"store_date"`
	CoverDate     string `json:"cover_date,omitempty"`
	CoverURL      string `json:"cover_url,omitempty"`
	Writers       string `json:"writers,omitempty"`
	Artists       string `json:"artists,omitempty"`
	Publisher     string `json:"publisher,omitempty"`

	// Local data (if we have this series/issue)
	LocalSeriesID *int64 `json:"local_series_id,omitempty"`
	LocalIssueID  *int64 `json:"local_issue_id,omitempty"`
	HasFile       bool   `json:"has_file"`
	FileID        *int64 `json:"file_id,omitempty"`
	Tracked       bool   `json:"tracked"`
	Wanted        bool   `json:"wanted"`
}

// ReleaseDebugInfo provides diagnostic info about what data sources were used.
type ReleaseDebugInfo struct {
	Source         string `json:"source"`          // "walksoftly" or "comicvine"
	WalksoftlyCount int   `json:"walksoftly_count"`
	WalksoftlyError string `json:"walksoftly_error,omitempty"`
	CVFallbackCount int   `json:"cv_fallback_count,omitempty"`
	LocalCount     int    `json:"local_count"`
	TotalResults   int    `json:"total_results"`
	TrackedCount   int    `json:"tracked_count"`
	WeekNum        int    `json:"week_num,omitempty"`
}

// GetWeeklyReleases fetches all comics releasing in a date range.
// Primary source: walksoftly (pre-aggregated weekly data with ComicVine IDs).
// Fallback: ComicVine store_date API (works for past weeks).
// Always cross-references with local data for ownership/tracking status.
func (s *MetadataService) GetWeeklyReleases(startDate, endDate string) ([]PullListIssue, *ReleaseDebugInfo, error) {
	debug := &ReleaseDebugInfo{}

	// Build local data lookups (needed regardless of source)
	localByCV, localIssues := s.buildLocalIssueLookup(startDate, endDate)
	debug.LocalCount = len(localIssues)

	trackedSeriesByCV := s.buildTrackedSeriesLookup()
	debug.TrackedCount = len(trackedSeriesByCV)

	localSeriesByCV := s.buildLocalSeriesLookup()

	// Build want list lookup for cross-referencing
	wantedIssueIDs := s.buildWantedIssueLookup()

	// Build tracked series issue lookup: (seriesCVID, issueNumber) → local issue
	// This lets us match walksoftly releases when the issue-level CV ID is missing
	trackedIssuesByKey := s.buildTrackedIssuesLookup(trackedSeriesByCV)

	// Try walksoftly first (primary source)
	var results []PullListIssue
	weekNum, weekYear, err := walksoftly.DateToWeek(startDate)
	if err == nil {
		debug.WeekNum = weekNum
		wsReleases, wsErr := s.ws.GetWeeklyReleases(weekNum, weekYear)
		if wsErr != nil {
			slog.Warn("walksoftly fetch failed, will try ComicVine fallback", "error", wsErr)
			debug.WalksoftlyError = wsErr.Error()
		} else {
			debug.WalksoftlyCount = len(wsReleases)
			debug.Source = "walksoftly"
			results = s.buildResultsFromWalksoftly(wsReleases, localByCV, trackedSeriesByCV, localSeriesByCV, wantedIssueIDs, trackedIssuesByKey)
		}
	}

	// Fallback to ComicVine store_date if walksoftly failed or returned empty
	if len(results) == 0 && debug.Source != "walksoftly" {
		if !s.cv.HasAPIKey() {
			return nil, debug, fmt.Errorf("ComicVine API key not configured and walksoftly unavailable")
		}
		cvResults, cvCount := s.buildResultsFromComicVine(startDate, endDate, localByCV, trackedSeriesByCV, localSeriesByCV, wantedIssueIDs)
		results = cvResults
		debug.CVFallbackCount = cvCount
		if debug.Source == "" {
			debug.Source = "comicvine"
		}
	}

	// Supplement with local-only issues not in primary results
	seen := make(map[int]bool)
	for _, r := range results {
		if r.ComicVineID > 0 {
			seen[r.ComicVineID] = true
		}
	}
	localSupp := s.buildLocalOnlyResults(localIssues, seen, trackedSeriesByCV, wantedIssueIDs)
	results = append(results, localSupp...)

	debug.TotalResults = len(results)
	return results, debug, nil
}

// buildLocalIssueLookup loads local issues by store_date range and returns
// a map by ComicVine ID for cross-referencing, plus the full slice.
func (s *MetadataService) buildLocalIssueLookup(startDate, endDate string) (map[int64]*model.Issue, []model.Issue) {
	localByCV := make(map[int64]*model.Issue)
	localIssues, err := s.issueRepo.ListByDateRange(startDate, endDate, false)
	if err != nil {
		slog.Warn("failed to load local issues", "error", err)
		return localByCV, nil
	}
	for i := range localIssues {
		if localIssues[i].ComicVineID != nil {
			localByCV[*localIssues[i].ComicVineID] = &localIssues[i]
		}
	}
	return localByCV, localIssues
}

// buildTrackedSeriesLookup returns a map of tracked series by ComicVine ID.
func (s *MetadataService) buildTrackedSeriesLookup() map[int64]*model.Series {
	trackedSeriesByCV := make(map[int64]*model.Series)
	tracked, err := s.seriesRepo.ListTracked()
	if err != nil {
		slog.Warn("failed to load tracked series", "error", err)
		return trackedSeriesByCV
	}
	for i := range tracked {
		if tracked[i].ComicVineID != nil {
			trackedSeriesByCV[*tracked[i].ComicVineID] = &tracked[i]
		}
	}
	return trackedSeriesByCV
}

// buildLocalSeriesLookup returns a map of all local series by ComicVine ID.
func (s *MetadataService) buildLocalSeriesLookup() map[int64]*model.Series {
	localSeriesByCV := make(map[int64]*model.Series)
	allSeries, _, err := s.seriesRepo.List(1, 10000, "title", "asc")
	if err != nil {
		slog.Warn("failed to load local series", "error", err)
		return localSeriesByCV
	}
	for i := range allSeries {
		if allSeries[i].ComicVineID != nil {
			localSeriesByCV[*allSeries[i].ComicVineID] = &allSeries[i]
		}
	}
	return localSeriesByCV
}

// buildTrackedIssuesLookup builds a map keyed by "seriesCVID:issueNumber" → local issue
// for all issues in tracked series. This enables cross-referencing walksoftly releases
// when the issue-level ComicVine ID is missing (common for future issues).
func (s *MetadataService) buildTrackedIssuesLookup(trackedSeriesByCV map[int64]*model.Series) map[string]*model.Issue {
	lookup := make(map[string]*model.Issue)
	for cvID, series := range trackedSeriesByCV {
		issues, err := s.issueRepo.ListBySeries(series.ID)
		if err != nil {
			slog.Warn("failed to load issues for tracked series", "series_id", series.ID, "error", err)
			continue
		}
		for i := range issues {
			key := fmt.Sprintf("%d:%s", cvID, issues[i].IssueNumber)
			lookup[key] = &issues[i]
		}
	}
	return lookup
}

// buildWantedIssueLookup returns a set of local issue IDs on the want list.
func (s *MetadataService) buildWantedIssueLookup() map[int64]bool {
	if s.wantListRepo == nil {
		return make(map[int64]bool)
	}
	wanted, err := s.wantListRepo.ListWantedIssueIDs()
	if err != nil {
		slog.Warn("failed to load wanted issue IDs", "error", err)
		return make(map[int64]bool)
	}
	return wanted
}

// buildResultsFromWalksoftly converts walksoftly releases to PullListIssues,
// cross-referenced with local data.
func (s *MetadataService) buildResultsFromWalksoftly(
	releases []walksoftly.Release,
	localByCV map[int64]*model.Issue,
	trackedSeriesByCV map[int64]*model.Series,
	localSeriesByCV map[int64]*model.Series,
	wantedIssueIDs map[int64]bool,
	trackedIssuesByKey map[string]*model.Issue,
) []PullListIssue {
	var results []PullListIssue

	for _, rel := range releases {
		item := PullListIssue{
			SeriesName:  rel.Series,
			IssueNumber: rel.Issue,
			StoreDate:   rel.ShipDate,
			Publisher:   rel.Publisher,
		}

		if rel.CoverDate != nil {
			item.CoverDate = *rel.CoverDate
		}

		// Parse ComicVine IDs (walksoftly returns them as strings)
		var seriesCVID int64
		if rel.ComicID != nil {
			if id, err := strconv.ParseInt(*rel.ComicID, 10, 64); err == nil && id > 0 {
				item.SeriesCVID = int(id)
				seriesCVID = id
			}
		}

		var issueCVID int64
		if rel.IssueID != nil {
			if id, err := strconv.ParseInt(*rel.IssueID, 10, 64); err == nil && id > 0 {
				item.ComicVineID = int(id)
				item.ComicVineURL = fmt.Sprintf("https://comicvine.gamespot.com/issue/4000-%d/", id)
				issueCVID = id
			}
		}

		// Cross-reference with local issue data (by issue CV ID first)
		var matchedLocal *model.Issue
		if issueCVID > 0 {
			if local, ok := localByCV[issueCVID]; ok {
				matchedLocal = local
			}
		}

		// Fallback: cross-reference by series CV ID + issue number for tracked series
		// (common for future issues where walksoftly has no issue-level CV ID)
		if matchedLocal == nil && seriesCVID > 0 {
			key := fmt.Sprintf("%d:%s", seriesCVID, rel.Issue)
			if local, ok := trackedIssuesByKey[key]; ok {
				matchedLocal = local
			}
		}

		if matchedLocal != nil {
			item.LocalIssueID = &matchedLocal.ID
			item.LocalSeriesID = &matchedLocal.SeriesID
			item.HasFile = matchedLocal.HasFile
			item.FileID = matchedLocal.FileID
			item.Wanted = wantedIssueIDs[matchedLocal.ID]
			// Use local cover URL if available (walksoftly doesn't provide images)
			if matchedLocal.CoverURL != "" {
				item.CoverURL = matchedLocal.CoverURL
			}
			// Use local title/writers/artists if available
			if matchedLocal.Title != "" {
				item.Title = matchedLocal.Title
			}
			if matchedLocal.Writers != "" {
				item.Writers = matchedLocal.Writers
			}
			if matchedLocal.Artists != "" {
				item.Artists = matchedLocal.Artists
			}
			// If we matched via series+number but had no issue CV ID, inherit it from local
			if item.ComicVineID == 0 && matchedLocal.ComicVineID != nil {
				item.ComicVineID = int(*matchedLocal.ComicVineID)
				item.ComicVineURL = fmt.Sprintf("https://comicvine.gamespot.com/issue/4000-%d/", *matchedLocal.ComicVineID)
			}
		}

		// Cross-reference with tracked series
		if seriesCVID > 0 {
			if ts, ok := trackedSeriesByCV[seriesCVID]; ok {
				item.Tracked = true
				if item.LocalSeriesID == nil {
					item.LocalSeriesID = &ts.ID
				}
			}
			// Enrich with local series cover URL if we don't have one yet
			if item.CoverURL == "" {
				if ls, ok := localSeriesByCV[seriesCVID]; ok {
					_ = ls // local series doesn't have cover URL on the series level
				}
			}
		}

		results = append(results, item)
	}

	return results
}

// buildResultsFromComicVine is the fallback path using ComicVine's store_date API.
func (s *MetadataService) buildResultsFromComicVine(
	startDate, endDate string,
	localByCV map[int64]*model.Issue,
	trackedSeriesByCV map[int64]*model.Series,
	localSeriesByCV map[int64]*model.Series,
	wantedIssueIDs map[int64]bool,
) ([]PullListIssue, int) {
	cvIssues, err := s.cv.GetIssuesByStoreDate(startDate, endDate)
	if err != nil {
		slog.Warn("ComicVine fallback failed", "error", err)
		return nil, 0
	}

	// Build publisher lookup from local data + cache
	publisherByVolumeID := make(map[int]string)
	for cvID, ls := range localSeriesByCV {
		if ls.PublisherName != "" {
			publisherByVolumeID[int(cvID)] = ls.PublisherName
			s.publisherCache[int(cvID)] = ls.PublisherName
		}
	}

	var uncachedVolumeIDs []int
	for _, cvIssue := range cvIssues {
		if cvIssue.Volume == nil || cvIssue.Volume.ID == 0 {
			continue
		}
		vid := cvIssue.Volume.ID
		if _, ok := publisherByVolumeID[vid]; ok {
			continue
		}
		if cached, ok := s.publisherCache[vid]; ok {
			publisherByVolumeID[vid] = cached
			continue
		}
		uncachedVolumeIDs = append(uncachedVolumeIDs, vid)
	}

	// Deduplicate and fetch
	uncachedSet := make(map[int]bool)
	var uniqueUncached []int
	for _, id := range uncachedVolumeIDs {
		if !uncachedSet[id] {
			uncachedSet[id] = true
			uniqueUncached = append(uniqueUncached, id)
		}
	}
	if len(uniqueUncached) > 0 {
		volumes, err := s.cv.GetVolumesByIDs(uniqueUncached)
		if err == nil {
			for _, v := range volumes {
				if v.Publisher != nil && v.Publisher.Name != "" {
					publisherByVolumeID[v.ID] = v.Publisher.Name
					s.publisherCache[v.ID] = v.Publisher.Name
				}
			}
		}
	}

	var results []PullListIssue
	seen := make(map[int]bool)

	for _, cvIssue := range cvIssues {
		if seen[cvIssue.ID] {
			continue
		}
		seen[cvIssue.ID] = true

		seriesName := ""
		seriesCVID := 0
		publisher := ""
		if cvIssue.Volume != nil {
			seriesName = cvIssue.Volume.Name
			seriesCVID = cvIssue.Volume.ID
			publisher = publisherByVolumeID[cvIssue.Volume.ID]
		}

		coverURL := ""
		if cvIssue.Image != nil {
			coverURL = cvIssue.Image.SmallURL
		}

		var writers, artists []string
		for _, pc := range cvIssue.PersonCredits {
			role := strings.ToLower(pc.Role)
			if strings.Contains(role, "writer") {
				writers = append(writers, pc.Name)
			}
			if strings.Contains(role, "artist") || strings.Contains(role, "pencil") ||
				strings.Contains(role, "ink") {
				artists = append(artists, pc.Name)
			}
		}

		desc := comicvine.StripHTML(cvIssue.Description)
		if len(desc) > 200 {
			desc = desc[:200] + "..."
		}

		displayDate := cvIssue.StoreDate
		if displayDate == "" {
			displayDate = cvIssue.CoverDate
		}

		item := PullListIssue{
			ComicVineID:  cvIssue.ID,
			ComicVineURL: fmt.Sprintf("https://comicvine.gamespot.com/issue/4000-%d/", cvIssue.ID),
			SeriesName:   seriesName,
			SeriesCVID:   seriesCVID,
			IssueNumber:  cvIssue.IssueNumber,
			Title:        cvIssue.Name,
			Description:  desc,
			StoreDate:    displayDate,
			CoverDate:    cvIssue.CoverDate,
			CoverURL:     coverURL,
			Writers:      strings.Join(writers, ", "),
			Artists:      strings.Join(artists, ", "),
			Publisher:    publisher,
		}

		cvID64 := int64(cvIssue.ID)
		if local, ok := localByCV[cvID64]; ok {
			item.LocalIssueID = &local.ID
			item.LocalSeriesID = &local.SeriesID
			item.HasFile = local.HasFile
			item.FileID = local.FileID
			item.Wanted = wantedIssueIDs[local.ID]
		}

		if seriesCVID > 0 {
			if ts, ok := trackedSeriesByCV[int64(seriesCVID)]; ok {
				item.Tracked = true
				if item.LocalSeriesID == nil {
					item.LocalSeriesID = &ts.ID
				}
			}
		}

		results = append(results, item)
	}

	return results, len(cvIssues)
}

// buildLocalOnlyResults adds local issues that weren't found in the primary results.
func (s *MetadataService) buildLocalOnlyResults(
	localIssues []model.Issue,
	seen map[int]bool,
	trackedSeriesByCV map[int64]*model.Series,
	wantedIssueIDs map[int64]bool,
) []PullListIssue {
	var results []PullListIssue

	for _, li := range localIssues {
		if li.ComicVineID != nil && seen[int(*li.ComicVineID)] {
			continue
		}

		publisher := ""
		if localSeries, err := s.seriesRepo.GetByID(li.SeriesID); err == nil && localSeries != nil {
			publisher = localSeries.PublisherName
		}

		displayDate := li.StoreDate
		if displayDate == "" {
			displayDate = li.CoverDate
		}

		item := PullListIssue{
			SeriesName:    li.SeriesTitle,
			IssueNumber:   li.IssueNumber,
			Title:         li.Title,
			StoreDate:     displayDate,
			CoverDate:     li.CoverDate,
			CoverURL:      li.CoverURL,
			Writers:       li.Writers,
			Artists:       li.Artists,
			Publisher:     publisher,
			LocalIssueID:  &li.ID,
			LocalSeriesID: &li.SeriesID,
			HasFile:       li.HasFile,
			FileID:        li.FileID,
			Wanted:        wantedIssueIDs[li.ID],
		}
		if li.ComicVineID != nil {
			item.ComicVineID = int(*li.ComicVineID)
			item.ComicVineURL = fmt.Sprintf("https://comicvine.gamespot.com/issue/4000-%d/", *li.ComicVineID)
		}

		if localSeries, err := s.seriesRepo.GetByID(li.SeriesID); err == nil && localSeries != nil {
			if localSeries.ComicVineID != nil {
				if _, ok := trackedSeriesByCV[*localSeries.ComicVineID]; ok {
					item.Tracked = true
				}
				item.SeriesCVID = int(*localSeries.ComicVineID)
			}
		}

		results = append(results, item)
	}

	return results
}

// TrackFromComicVine ensures a local series exists for the given ComicVine volume ID,
// populates it with metadata and issues from ComicVine, marks it as tracked,
// and adds missing issues to the want list. Returns the series and count of want list items added.
func (s *MetadataService) TrackFromComicVine(cvVolumeID int, wantListRepo *repository.WantListRepo) (*model.Series, int, error) {
	if !s.cv.HasAPIKey() {
		return nil, 0, fmt.Errorf("ComicVine API key not configured")
	}

	// Step 1: Check if a local series already exists for this CV volume
	cvID64 := int64(cvVolumeID)
	existing, err := s.seriesRepo.FindByComicVineID(cvID64)
	if err != nil {
		return nil, 0, fmt.Errorf("checking for existing series: %w", err)
	}

	var series *model.Series

	if existing != nil {
		series = existing
	} else {
		// Step 2: Fetch volume from ComicVine to get the series name
		volume, err := s.cv.GetVolume(cvVolumeID)
		if err != nil {
			return nil, 0, fmt.Errorf("fetching volume from ComicVine: %w", err)
		}

		// Step 3: Create a minimal local series record
		series = &model.Series{
			Title:     volume.Name,
			SortTitle: scanner.MakeSortTitle(volume.Name),
			Status:    "continuing",
		}
		if volume.StartYear != "" {
			var year int
			fmt.Sscanf(volume.StartYear, "%d", &year)
			if year > 0 {
				series.Year = &year
			}
		}
		if err := s.seriesRepo.Create(series); err != nil {
			return nil, 0, fmt.Errorf("creating series: %w", err)
		}

		// Step 4: Match the series to the ComicVine volume
		// (this updates series metadata and populates all issues)
		if err := s.MatchSeriesToVolume(series.ID, cvVolumeID); err != nil {
			return nil, 0, fmt.Errorf("matching series to volume: %w", err)
		}
	}

	// Step 5: Mark the series as tracked
	if err := s.seriesRepo.SetTracked(series.ID, true); err != nil {
		return nil, 0, fmt.Errorf("tracking series: %w", err)
	}

	// Step 6: Add missing issues to the want list
	wantAdded := 0
	if wantListRepo != nil {
		added, err := wantListRepo.AddMissingForSeries(series.ID)
		if err != nil {
			slog.Warn("failed to add missing issues to want list", "series_id", series.ID, "error", err)
		} else {
			wantAdded = added
		}
	}

	// Reload the series to get updated computed fields
	series, _ = s.seriesRepo.GetByID(series.ID)

	slog.Info("tracked series from ComicVine",
		"series_id", series.ID,
		"cv_volume_id", cvVolumeID,
		"title", series.Title,
		"want_added", wantAdded,
	)

	return series, wantAdded, nil
}

// WantIssueFromComicVine ensures a local issue exists for the given ComicVine issue ID
// and adds it to the want list. If the series doesn't exist locally, it creates it first.
// seriesCVID is required when the issue doesn't exist locally (needed to find/create the series).
func (s *MetadataService) WantIssueFromComicVine(cvIssueID int, seriesCVID int, wantListRepo *repository.WantListRepo) (*model.WantListItem, error) {
	if wantListRepo == nil {
		return nil, fmt.Errorf("want list repo is required")
	}

	// Step 1: Check if a local issue already exists for this CV issue ID
	cvIssueID64 := int64(cvIssueID)
	existing, err := s.issueRepo.FindByComicVineID(cvIssueID64)
	if err != nil {
		return nil, fmt.Errorf("checking for existing issue: %w", err)
	}

	if existing != nil {
		// Issue exists locally — just add to want list
		item, err := wantListRepo.Create(existing.ID, 0, "")
		if err != nil {
			return nil, fmt.Errorf("adding to want list: %w", err)
		}
		return item, nil
	}

	// Step 2: Issue doesn't exist locally — we need to create the series first
	if seriesCVID == 0 {
		return nil, fmt.Errorf("series ComicVine ID is required to create issue records")
	}

	// Ensure the series (and all its issues) exist locally
	_, _, err = s.TrackFromComicVine(seriesCVID, wantListRepo)
	if err != nil {
		return nil, fmt.Errorf("creating series from ComicVine: %w", err)
	}

	// Step 3: Now the issue should exist — look it up again
	existing, err = s.issueRepo.FindByComicVineID(cvIssueID64)
	if err != nil {
		return nil, fmt.Errorf("finding issue after series creation: %w", err)
	}
	if existing == nil {
		return nil, fmt.Errorf("issue with ComicVine ID %d not found after populating series", cvIssueID)
	}

	// Step 4: Add to want list (may already be there from TrackFromComicVine's AddMissingForSeries)
	item, err := wantListRepo.Create(existing.ID, 0, "")
	if err != nil {
		return nil, fmt.Errorf("adding to want list: %w", err)
	}
	return item, nil
}

// SetLibraryDir saves the library directory to the settings database.
func (s *MetadataService) SetLibraryDir(dir string) error {
	return s.settingRepo.Set("library_dir", dir)
}

// GetSettings returns all current settings for display.
func (s *MetadataService) GetSettings() map[string]interface{} {
	settings := map[string]interface{}{
		"comicvine_api_key_masked":   s.GetAPIKeyMasked(),
		"comicvine_api_key_source":   s.GetAPIKeySource(),
		"comicvine_api_key_set":      s.HasAPIKey(),
		"comicvine_hourly_remaining": s.HourlyRemaining(),
	}

	// Library dir: DB setting takes priority, config is fallback
	if libDir, err := s.settingRepo.Get("library_dir"); err == nil && libDir != "" {
		settings["library_dir"] = libDir
	} else {
		settings["library_dir"] = s.configLibraryDir
	}

	// Pull list schedule settings
	enabled, _ := s.settingRepo.Get("pull_list_enabled")
	settings["pull_list_enabled"] = enabled == "true"

	dayStr, _ := s.settingRepo.Get("pull_list_day")
	day := 3 // default: Wednesday
	if d, err := strconv.Atoi(dayStr); err == nil && d >= 0 && d <= 6 {
		day = d
	}
	settings["pull_list_day"] = day

	hourStr, _ := s.settingRepo.Get("pull_list_hour")
	hour := 6 // default: 6 AM
	if h, err := strconv.Atoi(hourStr); err == nil && h >= 0 && h <= 23 {
		hour = h
	}
	settings["pull_list_hour"] = hour

	lastRun, _ := s.settingRepo.Get("pull_list_last_run")
	settings["pull_list_last_run"] = lastRun

	return settings
}

// timestamp helper
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

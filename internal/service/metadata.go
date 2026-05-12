package service

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jeremy/longbox/internal/comicvine"
	"github.com/jeremy/longbox/internal/metron"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scanner"
	"github.com/jeremy/longbox/internal/walksoftly"
)

// MetadataService handles ComicVine + Metron metadata operations.
type MetadataService struct {
	cv               *comicvine.Client
	metron           *metron.Client
	ws               *walksoftly.Client
	seriesRepo       *repository.SeriesRepo
	issueRepo        *repository.IssueRepo
	publisherRepo    *repository.PublisherRepo
	wantListRepo     *repository.WantListRepo
	settingRepo      *repository.SettingRepo
	configAPIKey     string
	configMetronUser string
	configMetronTok  string
	configLibraryDir string

	// In-memory cache: ComicVine volume ID → publisher name.
	// Avoids re-fetching publisher data on every pull list page load.
	// Calendar/pull-list reads can come in concurrently (e.g., SSR prefetch
	// + browser request), so the cache needs a lock — naked map writes
	// from multiple goroutines abort the Go runtime.
	publisherCacheMu sync.RWMutex
	publisherCache   map[int]string
}

func NewMetadataService(
	cv *comicvine.Client,
	metronClient *metron.Client,
	ws *walksoftly.Client,
	seriesRepo *repository.SeriesRepo,
	issueRepo *repository.IssueRepo,
	publisherRepo *repository.PublisherRepo,
	wantListRepo *repository.WantListRepo,
	settingRepo *repository.SettingRepo,
	configAPIKey string,
	configMetronUser string,
	configMetronTok string,
	configLibraryDir string,
) *MetadataService {
	return &MetadataService{
		cv:               cv,
		metron:           metronClient,
		ws:               ws,
		seriesRepo:       seriesRepo,
		issueRepo:        issueRepo,
		publisherRepo:    publisherRepo,
		wantListRepo:     wantListRepo,
		settingRepo:      settingRepo,
		configAPIKey:     configAPIKey,
		configMetronUser: configMetronUser,
		configMetronTok:  configMetronTok,
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

// EnsureMetronCreds loads Metron credentials from settings (priority) or
// config and pushes them into the Metron client. Idempotent — safe to call
// at startup AND after a settings PUT.
func (s *MetadataService) EnsureMetronCreds() error {
	if s.metron == nil {
		return nil
	}
	user, _ := s.settingRepo.Get("metron_username")
	tok, _ := s.settingRepo.Get("metron_api_token")
	if user == "" {
		user = s.configMetronUser
	}
	if tok == "" {
		tok = s.configMetronTok
	}
	s.metron.SetCredentials(user, tok)
	return nil
}

// HasMetron returns true when Metron is reachable (credentials are set).
func (s *MetadataService) HasMetron() bool {
	return s.metron != nil && s.metron.HasCredentials()
}

// MetronStatus is the shape returned to the settings UI so users can see
// their Metron credential state + remaining quota at a glance.
type MetronStatus struct {
	Username        string `json:"username"`
	TokenMasked     string `json:"token_masked"`
	HasCredentials  bool   `json:"has_credentials"`
	BurstRemaining  int    `json:"burst_remaining"`
	DailyRemaining  int    `json:"daily_remaining"`
}

// GetMetronStatus returns a snapshot of the current Metron config + quota
// state. The token is masked for display.
func (s *MetadataService) GetMetronStatus() MetronStatus {
	st := MetronStatus{}
	if s.metron == nil {
		return st
	}
	user, _ := s.settingRepo.Get("metron_username")
	tok, _ := s.settingRepo.Get("metron_api_token")
	if user == "" {
		user = s.configMetronUser
	}
	if tok == "" {
		tok = s.configMetronTok
	}
	st.Username = user
	st.HasCredentials = s.metron.HasCredentials()
	st.BurstRemaining = s.metron.BurstRemaining()
	st.DailyRemaining = s.metron.DailyRemaining()
	if tok != "" {
		if len(tok) <= 8 {
			st.TokenMasked = "****"
		} else {
			st.TokenMasked = tok[:4] + "..." + tok[len(tok)-4:]
		}
	}
	return st
}

// TestMetronConnection runs a single authenticated request against Metron
// to verify credentials. Returns nil on success.
func (s *MetadataService) TestMetronConnection() error {
	if s.metron == nil {
		return fmt.Errorf("metron client not configured")
	}
	return s.metron.TestConnection()
}

// FindIssueByCVID finds a local issue by its ComicVine ID.
func (s *MetadataService) FindIssueByCVID(cvID int64) (*model.Issue, error) {
	return s.issueRepo.FindByComicVineID(cvID)
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

// SetMetronCredentials persists Metron credentials to settings and
// updates the live client. Either field may be empty to clear it.
func (s *MetadataService) SetMetronCredentials(username, token string) error {
	if err := s.settingRepo.Set("metron_username", strings.TrimSpace(username)); err != nil {
		return fmt.Errorf("saving metron_username: %w", err)
	}
	if err := s.settingRepo.Set("metron_api_token", strings.TrimSpace(token)); err != nil {
		return fmt.Errorf("saving metron_api_token: %w", err)
	}
	if s.metron != nil {
		s.metron.SetCredentials(username, token)
	}
	return nil
}

// HourlyRemaining returns how many API calls are left this hour.
func (s *MetadataService) HourlyRemaining() int {
	return s.cv.HourlyRemaining()
}

// MetadataSearchResult wraps a volume/series search hit from ComicVine
// and/or Metron. A row is the merged view of the same series — if both
// sources return it, ComicVineID and MetronID are both populated and
// Sources lists the providers that contributed.
//
// The merge happens by normalized (name, year). Cover URL preference is
// Metron-first to match the per-cover preference (Metron's covers are
// higher-res). Description prefers whichever source returned a non-empty
// value, again preferring Metron.
type MetadataSearchResult struct {
	ComicVineID  int      `json:"comicvine_id,omitempty"`
	MetronID     int      `json:"metron_id,omitempty"`
	Sources      []string `json:"sources"` // "comicvine", "metron"
	Name         string   `json:"name"`
	StartYear    string   `json:"start_year"`
	IssueCount   int      `json:"issue_count"`
	Publisher    string   `json:"publisher"`
	Description  string   `json:"description"`
	ImageURL     string   `json:"image_url"`
	ResourceType string   `json:"resource_type"`
	MatchScore   int      `json:"match_score"`
}

// SearchVolumes searches BOTH ComicVine and Metron for series matching a
// query and returns the union. Same-series rows are merged into a single
// result with both source IDs populated; the caller picks which source
// gets used for the eventual Track action.
//
// If either source is unavailable (no API key / no credentials) the
// remaining source is queried alone. Total count is the union size — the
// page param is currently only honored by ComicVine (Metron returns the
// first page of its own results, which is sufficient for the search UI).
func (s *MetadataService) SearchVolumes(query string, page int) ([]MetadataSearchResult, int, error) {
	cvAvailable := s.cv.HasAPIKey()
	metronAvailable := s.HasMetron()
	if !cvAvailable && !metronAvailable {
		return nil, 0, fmt.Errorf("no metadata source configured — set ComicVine API key or Metron credentials in Settings")
	}

	type cvOut struct {
		results []comicvine.SearchResult
		total   int
		err     error
	}
	type metronOut struct {
		results []metron.SearchResult
		err     error
	}

	var (
		cvCh     = make(chan cvOut, 1)
		metronCh = make(chan metronOut, 1)
	)
	if cvAvailable {
		go func() {
			r, t, err := s.cv.SearchVolumes(query, page)
			cvCh <- cvOut{r, t, err}
		}()
	} else {
		cvCh <- cvOut{}
	}
	if metronAvailable {
		go func() {
			r, err := s.metron.SearchSeries(query)
			metronCh <- metronOut{r, err}
		}()
	} else {
		metronCh <- metronOut{}
	}

	cv := <-cvCh
	mt := <-metronCh

	// If both errored, surface the first error rather than an empty list.
	if cv.err != nil && (mt.err != nil || !metronAvailable) {
		return nil, 0, cv.err
	}
	if cv.err != nil {
		slog.Warn("comicvine search failed; using metron only", "error", cv.err)
	}
	if mt.err != nil {
		slog.Warn("metron search failed; using comicvine only", "error", mt.err)
	}

	// Build by (normalized name, year) key so duplicates from both sources
	// collapse into one row with both IDs populated.
	//
	// Normalization rules — discovered empirically against the live Metron
	// API (see commit message of the merge-tightening commit):
	//   * Lowercase + trim whitespace.
	//   * Strip a trailing " (YYYY)" / " (YYYY-)" / " (YYYY-YYYY)" suffix —
	//     Metron's list endpoint formats names that way, ComicVine doesn't.
	//   * Collapse all internal whitespace runs to single spaces.
	//
	// The year axis stays separate so multi-volume series ("Batman" 1940
	// vs "Batman" 2016) don't collapse into one row.
	merged := make(map[string]*MetadataSearchResult)
	order := []string{}

	addKey := func(name, year string) string {
		return normalizeSearchKey(name) + "|" + year
	}

	for _, r := range cv.results {
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
		key := addKey(r.Name, r.StartYear)
		if existing, ok := merged[key]; ok {
			existing.ComicVineID = r.ID
			existing.Sources = appendUnique(existing.Sources, "comicvine")
			if existing.Description == "" {
				existing.Description = desc
			}
			if existing.IssueCount == 0 {
				existing.IssueCount = r.CountOfIssues
			}
			if existing.Publisher == "" {
				existing.Publisher = publisher
			}
			continue
		}
		merged[key] = &MetadataSearchResult{
			ComicVineID:  r.ID,
			Sources:      []string{"comicvine"},
			Name:         r.Name,
			StartYear:    r.StartYear,
			IssueCount:   r.CountOfIssues,
			Publisher:    publisher,
			Description:  desc,
			ImageURL:     imageURL,
			ResourceType: r.ResourceType,
		}
		order = append(order, key)
	}

	for _, r := range mt.results {
		year := ""
		if r.YearStarted > 0 {
			year = fmt.Sprintf("%d", r.YearStarted)
		}
		key := addKey(r.Name, year)
		desc := strings.TrimSpace(r.Description)
		if len(desc) > 300 {
			desc = desc[:300] + "..."
		}
		if existing, ok := merged[key]; ok {
			existing.MetronID = r.ID
			existing.Sources = appendUnique(existing.Sources, "metron")
			// Metron-preferred for cover + description (when present).
			if strings.TrimSpace(r.ImageURL) != "" {
				existing.ImageURL = r.ImageURL
			}
			if desc != "" {
				existing.Description = desc
			}
			if existing.IssueCount == 0 {
				existing.IssueCount = r.IssueCount
			}
			if existing.Publisher == "" {
				existing.Publisher = r.PublisherName
			}
			continue
		}
		merged[key] = &MetadataSearchResult{
			MetronID:     r.ID,
			Sources:      []string{"metron"},
			Name:         r.Name,
			StartYear:    year,
			IssueCount:   r.IssueCount,
			Publisher:    r.PublisherName,
			Description:  desc,
			ImageURL:     r.ImageURL,
			ResourceType: "volume",
		}
		order = append(order, key)
	}

	out := make([]MetadataSearchResult, 0, len(order))
	for _, k := range order {
		out = append(out, *merged[k])
	}
	return out, len(out), nil
}

func appendUnique(slice []string, v string) []string {
	for _, x := range slice {
		if x == v {
			return slice
		}
	}
	return append(slice, v)
}

// trailingYearSuffixRE matches " (YYYY)" / " (YYYY-)" / " (YYYY-YYYY)"
// at the end of a series name. Mirrors the helper in the metron client
// — duplicated to avoid an import cycle and so MetadataService can run
// the merge with no per-source-specific knowledge.
var trailingYearSuffixRE = regexp.MustCompile(`\s*\((\d{4})(?:-(?:\d{4})?)?\)\s*$`)

// multiSpaceRE collapses runs of whitespace.
var multiSpaceRE = regexp.MustCompile(`\s+`)

func normalizeSearchKey(name string) string {
	name = trailingYearSuffixRE.ReplaceAllString(name, "")
	name = multiSpaceRE.ReplaceAllString(name, " ")
	return strings.ToLower(strings.TrimSpace(name))
}

// GetVolume fetches volume details from ComicVine.
func (s *MetadataService) GetVolume(cvID int) (*comicvine.Volume, error) {
	if !s.cv.HasAPIKey() {
		return nil, fmt.Errorf("ComicVine API key not configured")
	}
	return s.cv.GetVolume(cvID)
}

// VolumeIssuePreview is a lightweight issue representation for browse previews.
type VolumeIssuePreview struct {
	ComicVineID int    `json:"comicvine_id"`
	IssueNumber string `json:"issue_number"`
	Title       string `json:"title,omitempty"`
	CoverDate   string `json:"cover_date,omitempty"`
	StoreDate   string `json:"store_date,omitempty"`
	CoverURL    string `json:"cover_url,omitempty"`
	Description string `json:"description,omitempty"`
}

// GetVolumeIssuesPreview fetches all issues for a ComicVine volume without persisting.
func (s *MetadataService) GetVolumeIssuesPreview(cvVolumeID int) ([]VolumeIssuePreview, error) {
	if !s.cv.HasAPIKey() {
		return nil, fmt.Errorf("ComicVine API key not configured")
	}

	cvIssues, err := s.cv.GetVolumeIssues(cvVolumeID)
	if err != nil {
		return nil, fmt.Errorf("fetching volume issues: %w", err)
	}

	var out []VolumeIssuePreview
	for _, ci := range cvIssues {
		coverURL := ""
		if ci.Image != nil {
			coverURL = ci.Image.SmallURL
		}
		desc := comicvine.StripHTML(ci.Description)
		if len(desc) > 300 {
			desc = desc[:300] + "..."
		}
		out = append(out, VolumeIssuePreview{
			ComicVineID: ci.ID,
			IssueNumber: ci.IssueNumber,
			Title:       ci.Name,
			CoverDate:   ci.CoverDate,
			StoreDate:   ci.StoreDate,
			CoverURL:    coverURL,
			Description: desc,
		})
	}

	return out, nil
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
		issueNumber := strings.TrimSpace(cvIssue.IssueNumber)
		if issueNumber == "" {
			issueNumber = fmt.Sprintf("CV-%d", cvIssue.ID)
			slog.Warn("comicvine issue missing number; using fallback",
				"series_id", series.ID,
				"cv_issue_id", cvIssue.ID,
				"fallback", issueNumber,
			)
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

func (s *MetadataService) ensureIssueFromComicVine(seriesID int64, cvIssueID int) error {
	if !s.cv.HasAPIKey() {
		return fmt.Errorf("ComicVine API key not configured")
	}

	cvIssue, err := s.cv.GetIssue(cvIssueID)
	if err != nil {
		return fmt.Errorf("fetching issue from ComicVine: %w", err)
	}

	issueNumber := strings.TrimSpace(cvIssue.IssueNumber)
	if issueNumber == "" {
		issueNumber = fmt.Sprintf("CV-%d", cvIssue.ID)
		slog.Warn("comicvine issue missing number; using fallback",
			"series_id", seriesID,
			"cv_issue_id", cvIssue.ID,
			"fallback", issueNumber,
		)
	}

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

	desc := comicvine.StripHTML(cvIssue.Description)
	if len(desc) > 300 {
		desc = desc[:300] + "..."
	}

	cvID := int64(cvIssue.ID)
	issue := &model.Issue{
		SeriesID:    seriesID,
		IssueNumber: issueNumber,
		SortNumber:  scanner.SortNumber(issueNumber),
		Title:       cvIssue.Name,
		ComicVineID: &cvID,
		Description: desc,
		CoverDate:   cvIssue.CoverDate,
		StoreDate:   cvIssue.StoreDate,
		CoverURL:    coverURL,
		Writers:     strings.Join(writers, ", "),
		Artists:     strings.Join(artists, ", "),
		ReadStatus:  "unread",
	}

	if err := s.issueRepo.Create(issue); err != nil {
		return fmt.Errorf("creating issue from ComicVine: %w", err)
	}

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
	ComicVineID  int    `json:"comicvine_id,omitempty"`
	ComicVineURL string `json:"comicvine_url,omitempty"`
	SeriesName   string `json:"series_name"`
	SeriesCVID   int    `json:"series_cv_id,omitempty"`
	IssueNumber  string `json:"issue_number"`
	Title        string `json:"title,omitempty"`
	Description  string `json:"description,omitempty"`
	StoreDate    string `json:"store_date"`
	CoverDate    string `json:"cover_date,omitempty"`
	CoverURL     string `json:"cover_url,omitempty"`
	Writers      string `json:"writers,omitempty"`
	Artists      string `json:"artists,omitempty"`
	Publisher    string `json:"publisher,omitempty"`

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
	Source          string `json:"source"` // "walksoftly" or "comicvine"
	WalksoftlyCount int    `json:"walksoftly_count"`
	WalksoftlyError string `json:"walksoftly_error,omitempty"`
	CVFallbackCount int    `json:"cv_fallback_count,omitempty"`
	LocalCount      int    `json:"local_count"`
	TotalResults    int    `json:"total_results"`
	TrackedCount    int    `json:"tracked_count"`
	WeekNum         int    `json:"week_num,omitempty"`
}

// GetWeeklyReleases fetches all comics releasing in a date range.
// Primary source: walksoftly (pre-aggregated weekly data with ComicVine IDs).
// Fallback: ComicVine store_date API (works for past weeks).
// Always cross-references with local data for ownership/tracking status.
func (s *MetadataService) GetWeeklyReleases(startDate, endDate string) ([]PullListIssue, *ReleaseDebugInfo, error) {
	debug := &ReleaseDebugInfo{}

	// Build local data lookups (needed regardless of source)
	localByCV, localIssues := s.buildLocalIssueLookup(startDate, endDate)
	trackedSeriesByTitle := s.buildTrackedSeriesByTitle()
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
			results = s.buildResultsFromWalksoftly(wsReleases, localByCV, trackedSeriesByCV, trackedSeriesByTitle, localSeriesByCV, wantedIssueIDs, trackedIssuesByKey)
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

	// Supplement with local-only issues not in primary results.
	// The seen-set tracks BOTH issue CV IDs and (series CV ID, issue number)
	// keys; without the second key, a local issue whose series matches a
	// walksoftly release but whose issue-level CV ID is absent (common for
	// future issues) gets re-emitted as a "local-only" duplicate.
	seenCV := make(map[int]bool)
	seenSeriesIssue := make(map[string]bool)
	for _, r := range results {
		if r.ComicVineID > 0 {
			seenCV[r.ComicVineID] = true
		}
		if r.SeriesCVID > 0 && r.IssueNumber != "" {
			seenSeriesIssue[fmt.Sprintf("%d:%s", r.SeriesCVID, r.IssueNumber)] = true
		}
	}
	localSupp := s.buildLocalOnlyResults(localIssues, seenCV, seenSeriesIssue, trackedSeriesByCV, wantedIssueIDs)
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

// buildTrackedSeriesByTitle returns a fallback map of tracked series keyed by
// normalized title for cases where the walksoftly release lacks a series CV
// ID OR the local series row has no comicvine_id linkage. Defense-in-depth:
// without this fallback, a tracked-but-unlinked series shows up in the
// calendar with no "Tracked" badge.
//
// Key is normSeriesTitle(Title). Only one entry per key is kept — if two
// tracked series collide on the normalized title (e.g. multiple volumes
// of the same name), the entry is dropped from the fallback map so we
// don't pick the wrong volume.
func (s *MetadataService) buildTrackedSeriesByTitle() map[string]*model.Series {
	byTitle := make(map[string]*model.Series)
	collisions := make(map[string]bool)
	tracked, err := s.seriesRepo.ListTracked()
	if err != nil {
		return byTitle
	}
	for i := range tracked {
		key := normSeriesTitle(tracked[i].Title)
		if key == "" {
			continue
		}
		if _, exists := byTitle[key]; exists {
			collisions[key] = true
			continue
		}
		byTitle[key] = &tracked[i]
	}
	for k := range collisions {
		delete(byTitle, k)
	}
	return byTitle
}

// normSeriesTitle lowercases and strips everything except [a-z0-9] so that
// punctuation differences ("Doctor Strange" vs "Dr. Strange") and casing
// don't break series matching. Same shape as the word-boundary helper in
// the search service.
func normSeriesTitle(title string) string {
	out := make([]byte, 0, len(title))
	for i := 0; i < len(title); i++ {
		c := title[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			out = append(out, c)
		}
	}
	return string(out)
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
//
// Walksoftly's feed returns the same release multiple times when its source
// data lists the publisher under different names ("Image" vs "Image Comics",
// "Marvel" vs "Marvel Comics", "Boom! Studios" vs "BOOM! Studios"), or when
// a release is mirrored under both a canonical and a regional publisher
// imprint. Without dedup the pull-list UI shows each affected series two or
// three times — once per publisher spelling — and the per-week
// Tracked/Wanted/Owned counts are inflated.
//
// Dedupe key, in order of preference:
//  1. Issue-level ComicVine ID, if present
//  2. (series CV ID, issue number) for future issues without an issue CV ID
//  3. (normalized series name, issue number) as a last resort
//
// When a duplicate is seen we keep the first item but upgrade its Publisher
// to whichever spelling is longer (more specific), so "Image Comics" wins
// over "Image" and "Dynamite Entertainment" wins over "Dynamite".
func (s *MetadataService) buildResultsFromWalksoftly(
	releases []walksoftly.Release,
	localByCV map[int64]*model.Issue,
	trackedSeriesByCV map[int64]*model.Series,
	trackedSeriesByTitle map[string]*model.Series,
	localSeriesByCV map[int64]*model.Series,
	wantedIssueIDs map[int64]bool,
	trackedIssuesByKey map[string]*model.Issue,
) []PullListIssue {
	var results []PullListIssue
	dedupe := make(map[string]int) // canonical key → index into results

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

		// Cross-reference with tracked series — first by CV volume ID, then
		// fall back to normalized series title for tracked series without
		// CV linkage (rare but possible when the user tracks a series
		// without a successful ComicVine match).
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
		if !item.Tracked {
			if ts, ok := trackedSeriesByTitle[normSeriesTitle(rel.Series)]; ok {
				item.Tracked = true
				if item.LocalSeriesID == nil {
					item.LocalSeriesID = &ts.ID
				}
			}
		}

		// Dedupe key: prefer issue CV ID, then series CV ID + number, then
		// normalized series + number. Without this, the same release shows
		// up multiple times under publisher-name variants.
		var key string
		switch {
		case item.ComicVineID > 0:
			key = fmt.Sprintf("cv:%d", item.ComicVineID)
		case item.SeriesCVID > 0 && item.IssueNumber != "":
			key = fmt.Sprintf("scv:%d:%s", item.SeriesCVID, item.IssueNumber)
		default:
			key = fmt.Sprintf("ns:%s:%s", normSeriesTitle(item.SeriesName), item.IssueNumber)
		}

		if existing, ok := dedupe[key]; ok {
			// Same release came in under a different publisher spelling.
			// Prefer the longer (more specific) name — "Marvel Comics" over
			// "Marvel", "Dynamite Entertainment" over "Dynamite".
			if len(item.Publisher) > len(results[existing].Publisher) {
				results[existing].Publisher = item.Publisher
			}
			continue
		}
		dedupe[key] = len(results)
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
	s.publisherCacheMu.Lock()
	for cvID, ls := range localSeriesByCV {
		if ls.PublisherName != "" {
			publisherByVolumeID[int(cvID)] = ls.PublisherName
			s.publisherCache[int(cvID)] = ls.PublisherName
		}
	}
	s.publisherCacheMu.Unlock()

	var uncachedVolumeIDs []int
	s.publisherCacheMu.RLock()
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
	s.publisherCacheMu.RUnlock()

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
			s.publisherCacheMu.Lock()
			for _, v := range volumes {
				if v.Publisher != nil && v.Publisher.Name != "" {
					publisherByVolumeID[v.ID] = v.Publisher.Name
					s.publisherCache[v.ID] = v.Publisher.Name
				}
			}
			s.publisherCacheMu.Unlock()
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
//
// Skips a local issue if EITHER the issue CV ID OR the (series CV ID,
// issue number) tuple was already emitted upstream. The two-key check
// matters for future issues, where walksoftly has the series CV ID but
// not the issue CV ID — keying only on issue CV ID would let the local
// row be re-emitted as a duplicate.
func (s *MetadataService) buildLocalOnlyResults(
	localIssues []model.Issue,
	seenCV map[int]bool,
	seenSeriesIssue map[string]bool,
	trackedSeriesByCV map[int64]*model.Series,
	wantedIssueIDs map[int64]bool,
) []PullListIssue {
	var results []PullListIssue
	emitted := make(map[int64]bool) // protects against duplicate localIssues rows

	for _, li := range localIssues {
		if emitted[li.ID] {
			continue
		}
		if li.ComicVineID != nil && seenCV[int(*li.ComicVineID)] {
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

		// Second dedupe check: skip if upstream already emitted this
		// (series CV ID, issue number) tuple — typical for future issues
		// where walksoftly has the series but not the issue CV ID.
		if item.SeriesCVID > 0 && item.IssueNumber != "" {
			if seenSeriesIssue[fmt.Sprintf("%d:%s", item.SeriesCVID, item.IssueNumber)] {
				continue
			}
		}

		emitted[li.ID] = true
		results = append(results, item)
	}

	return results
}

// TrackFromComicVine ensures a local series exists for the given ComicVine volume ID,
// populates it with metadata and issues from ComicVine, marks it as tracked,
// and adds missing issues to the want list. Returns the series and count of want list items added.
func (s *MetadataService) TrackFromComicVine(cvVolumeID int, wantListRepo *repository.WantListRepo, wantAll ...bool) (*model.Series, int, error) {
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

	// Step 6: Add missing issues to the want list (unless wantAll=false)
	wantAdded := 0
	shouldWantAll := len(wantAll) == 0 || wantAll[0]
	if wantListRepo != nil && shouldWantAll {
		added, err := wantListRepo.AddMissingForSeries(series.ID)
		if err != nil {
			slog.Warn("failed to add missing issues to want list", "series_id", series.ID, "error", err)
		} else {
			wantAdded = added
		}
	}

	// Reload the series to get updated computed fields. Tolerate a reload
	// miss — if GetByID errors or returns nil, fall back to the in-memory
	// series we already have rather than panicking on the slog deref.
	if reloaded, err := s.seriesRepo.GetByID(series.ID); err != nil {
		slog.Warn("failed to reload series after tracking", "series_id", series.ID, "error", err)
	} else if reloaded != nil {
		series = reloaded
	}

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
	series, _, err := s.TrackFromComicVine(seriesCVID, wantListRepo)
	if err != nil {
		return nil, fmt.Errorf("creating series from ComicVine: %w", err)
	}

	// Step 3: Now the issue should exist — look it up again
	existing, err = s.issueRepo.FindByComicVineID(cvIssueID64)
	if err != nil {
		return nil, fmt.Errorf("finding issue after series creation: %w", err)
	}
	if existing == nil {
		if series == nil {
			return nil, fmt.Errorf("issue with ComicVine ID %d not found and series missing", cvIssueID)
		}
		if err := s.ensureIssueFromComicVine(series.ID, cvIssueID); err != nil {
			return nil, fmt.Errorf("creating issue from ComicVine: %w", err)
		}
		existing, err = s.issueRepo.FindByComicVineID(cvIssueID64)
		if err != nil {
			return nil, fmt.Errorf("finding issue after direct create: %w", err)
		}
		if existing == nil {
			return nil, fmt.Errorf("issue with ComicVine ID %d not found after direct create", cvIssueID)
		}
	}

	// Step 4: Add to want list (may already be there from TrackFromComicVine's AddMissingForSeries)
	item, err := wantListRepo.Create(existing.ID, 0, "")
	if err != nil {
		return nil, fmt.Errorf("adding to want list: %w", err)
	}
	return item, nil
}

// WantIssueBySeriesAndNumber tracks a series by its ComicVine volume ID, finds the
// issue by number, and adds it to the want list. Used for future releases that don't
// have an issue-level ComicVine ID yet.
func (s *MetadataService) WantIssueBySeriesAndNumber(seriesCVID int, issueNumber string, wantListRepo *repository.WantListRepo) (*model.WantListItem, error) {
	if wantListRepo == nil {
		return nil, fmt.Errorf("want list repo is required")
	}
	if seriesCVID == 0 {
		return nil, fmt.Errorf("series ComicVine ID is required")
	}

	// Ensure the series (and all its issues) exist locally
	series, _, err := s.TrackFromComicVine(seriesCVID, wantListRepo)
	if err != nil {
		return nil, fmt.Errorf("creating series from ComicVine: %w", err)
	}

	// Find the issue by series + number
	issue, err := s.issueRepo.FindBySeriesAndNumber(series.ID, issueNumber)
	if err != nil {
		return nil, fmt.Errorf("finding issue by number: %w", err)
	}

	// Future issues may not exist on ComicVine yet (walksoftly gets data from
	// publisher solicitations). Create a minimal issue record so it can be wanted.
	if issue == nil {
		issue = &model.Issue{
			SeriesID:    series.ID,
			IssueNumber: issueNumber,
			SortNumber:  scanner.SortNumber(issueNumber),
			ReadStatus:  "unread",
		}
		if err := s.issueRepo.Create(issue); err != nil {
			return nil, fmt.Errorf("creating issue #%s: %w", issueNumber, err)
		}
		slog.Info("created placeholder issue for future release",
			"series_id", series.ID,
			"issue_number", issueNumber,
		)
	}

	// Add to want list (may already be there from TrackFromComicVine's AddMissingForSeries)
	item, err := wantListRepo.Create(issue.ID, 0, "")
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
	mst := s.GetMetronStatus()
	settings := map[string]interface{}{
		"comicvine_api_key_masked":   s.GetAPIKeyMasked(),
		"comicvine_api_key_source":   s.GetAPIKeySource(),
		"comicvine_api_key_set":      s.HasAPIKey(),
		"comicvine_hourly_remaining": s.HourlyRemaining(),
		"metron_username":            mst.Username,
		"metron_token_masked":        mst.TokenMasked,
		"metron_token_set":           mst.HasCredentials,
		"metron_burst_remaining":     mst.BurstRemaining,
		"metron_sustained_remaining": mst.DailyRemaining,
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

	// Auto-search on want list add
	autoSearch, _ := s.settingRepo.Get("auto_search_on_add")
	settings["auto_search_on_add"] = autoSearch == "true"

	// Auto scan settings
	autoScanEnabled, _ := s.settingRepo.Get("auto_scan_enabled")
	settings["auto_scan_enabled"] = autoScanEnabled == "true"

	autoScanIntervalStr, _ := s.settingRepo.Get("auto_scan_interval")
	autoScanInterval := 60 // default: 60 minutes
	if i, err := strconv.Atoi(autoScanIntervalStr); err == nil && i >= 5 && i <= 1440 {
		autoScanInterval = i
	}
	settings["auto_scan_interval"] = autoScanInterval

	autoScanLastRun, _ := s.settingRepo.Get("auto_scan_last_run")
	settings["auto_scan_last_run"] = autoScanLastRun

	// Missing issue search settings
	missingEnabled, _ := s.settingRepo.Get("missing_search_enabled")
	settings["missing_search_enabled"] = missingEnabled == "true"

	missingIntervalStr, _ := s.settingRepo.Get("missing_search_interval")
	missingInterval := 10 // default: 10 minutes
	if i, err := strconv.Atoi(missingIntervalStr); err == nil && i >= 1 && i <= 1440 {
		missingInterval = i
	}
	settings["missing_search_interval"] = missingInterval

	missingLastRun, _ := s.settingRepo.Get("missing_search_last_run")
	settings["missing_search_last_run"] = missingLastRun

	// Post-process script
	postProcessScript, _ := s.settingRepo.Get("post_process_script")
	settings["post_process_script"] = postProcessScript

	// Backup settings
	backupOnStart, _ := s.settingRepo.Get("backup_on_start")
	settings["backup_on_start"] = backupOnStart == "true"

	backupRetentionStr, _ := s.settingRepo.Get("backup_retention")
	backupRetention := 5
	if r, err := strconv.Atoi(backupRetentionStr); err == nil && r > 0 {
		backupRetention = r
	}
	settings["backup_retention"] = backupRetention

	return settings
}

// timestamp helper
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jeremy/longbox/internal/comicvine"
	"github.com/jeremy/longbox/internal/metron"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scanner"
	"github.com/jeremy/longbox/internal/walksoftly"
)

// MetadataService handles ComicVine and Metron metadata operations.
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
	configLibraryDir string

	// In-memory cache: ComicVine volume ID → publisher name.
	// Avoids re-fetching publisher data on every pull list page load.
	publisherCache map[int]string
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
		configLibraryDir: configLibraryDir,
		publisherCache:   make(map[int]string),
	}
}

// EnsureMetronCredentials loads Metron username + token from settings if
// configured. Safe to call repeatedly.
func (s *MetadataService) EnsureMetronCredentials() error {
	if s.metron == nil {
		return nil
	}
	user, _ := s.settingRepo.Get("metron_username")
	token, _ := s.settingRepo.Get("metron_api_token")
	s.metron.SetCredentials(user, token)
	return nil
}

// HasMetronCredentials reports whether the Metron client is configured.
func (s *MetadataService) HasMetronCredentials() bool {
	return s.metron != nil && s.metron.HasCredentials()
}

// SetMetronCredentials persists username + token to settings and pushes them
// onto the live client so the next request uses them.
func (s *MetadataService) SetMetronCredentials(username, token string) error {
	if err := s.settingRepo.Set("metron_username", username); err != nil {
		return fmt.Errorf("saving metron username: %w", err)
	}
	if err := s.settingRepo.Set("metron_api_token", token); err != nil {
		return fmt.Errorf("saving metron token: %w", err)
	}
	if s.metron != nil {
		s.metron.SetCredentials(username, token)
	}
	return nil
}

// MetronUsername returns the configured Metron username (not secret).
func (s *MetadataService) MetronUsername() string {
	v, _ := s.settingRepo.Get("metron_username")
	return v
}

// MetronTokenMasked returns "••••••" if a token is configured, empty otherwise.
func (s *MetadataService) MetronTokenMasked() string {
	v, _ := s.settingRepo.Get("metron_api_token")
	if v == "" {
		return ""
	}
	return "••••••"
}

// MetronTokenSet reports whether a Metron API token is currently saved.
func (s *MetadataService) MetronTokenSet() bool {
	v, _ := s.settingRepo.Get("metron_api_token")
	return v != ""
}

// MetronQuota returns the most recent rate-limit snapshot from Metron.
func (s *MetadataService) MetronQuota() metron.QuotaSnapshot {
	if s.metron == nil {
		return metron.QuotaSnapshot{}
	}
	return s.metron.Quota()
}

// TestMetron makes a tiny Metron call (1-result series list) to verify that
// the configured credentials work. Returns the X-RateLimit observations as
// a side-effect (visible via MetronQuota afterwards).
func (s *MetadataService) TestMetron(ctx context.Context) error {
	if s.metron == nil || !s.metron.HasCredentials() {
		return fmt.Errorf("Metron credentials not configured")
	}
	params := url.Values{}
	params.Set("page_size", "1")
	if _, err := s.metron.SearchSeries(ctx, params); err != nil {
		return err
	}
	return nil
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

// HourlyRemaining returns how many API calls are left this hour.
func (s *MetadataService) HourlyRemaining() int {
	return s.cv.HourlyRemaining()
}

// CVNextResetIn returns the duration until the ComicVine hourly quota window
// resets. Useful for surfacing wait ETAs to the user.
func (s *MetadataService) CVNextResetIn() time.Duration {
	return s.cv.NextResetIn()
}

// SearchResult wraps ComicVine search results with match scoring.
type MetadataSearchResult struct {
	ComicVineID  int    `json:"comicvine_id"`
	Name         string `json:"name"`
	StartYear    string `json:"start_year"`
	IssueCount   int    `json:"issue_count"`
	Publisher    string `json:"publisher"`
	Description  string `json:"description"`
	ImageURL     string `json:"image_url"`
	ResourceType string `json:"resource_type"`
	MatchScore   int    `json:"match_score"`
}

// SearchVolumes searches ComicVine for volumes matching a query.
func (s *MetadataService) SearchVolumes(query string, page int) ([]MetadataSearchResult, int, error) {
	if !s.cv.HasAPIKey() {
		return nil, 0, fmt.Errorf("ComicVine API key not configured")
	}

	results, total, err := s.cv.SearchVolumes(context.Background(), query, page)
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
	return s.cv.GetVolume(context.Background(), cvID)
}

// BackfillSeriesCoverURL fetches just the cover URL for a series and persists
// it. Cheaper than RefreshSeriesAuto — does not touch issues. CV is preferred
// (one volume call carries the URL); Metron is used when CV is unavailable
// (Metron series detail doesn't expose a cover, so this falls through to the
// first issue's cover when needed). Returns an error if no provider is
// configured or matched, or if the chosen provider call fails.
func (s *MetadataService) BackfillSeriesCoverURL(ctx context.Context, seriesID int64) error {
	series, err := s.seriesRepo.GetByID(seriesID)
	if err != nil {
		return fmt.Errorf("getting series: %w", err)
	}
	if series == nil {
		return fmt.Errorf("series %d not found", seriesID)
	}

	hasCV := series.ComicVineID != nil && s.cv.HasAPIKey()
	hasMetron := series.MetronID != nil && s.HasMetronCredentials()
	if !hasCV && !hasMetron {
		return fmt.Errorf("series is not matched to any metadata provider")
	}

	tryCV := func() (string, error) {
		volume, err := s.cv.GetVolume(ctx, int(*series.ComicVineID))
		if err != nil {
			return "", err
		}
		if volume.Image == nil {
			return "", nil
		}
		return bestImageURL(volume.Image), nil
	}

	tryMetron := func() (string, error) {
		// Cover-only: hit the first page of /issue/?series_id=X ordered by
		// cover_date and grab the first issue's Image. Single HTTP call vs
		// the full pagination GetSeriesIssues does — saves up to ~10s of
		// minInterval-spaced calls per series and dramatically reduces the
		// daily 5000-call sustained Metron quota burn during a poster
		// refresh of hundreds of series.
		params := url.Values{}
		params.Set("series_id", fmt.Sprintf("%d", *series.MetronID))
		params.Set("ordering", "cover_date")
		resp, err := s.metron.SearchIssues(ctx, params)
		if err != nil {
			return "", err
		}
		for _, mi := range resp.Results {
			if mi.Image != "" {
				return mi.Image, nil
			}
		}
		return "", nil
	}

	var url string
	if hasCV {
		u, cvErr := tryCV()
		if cvErr == nil {
			url = u
		} else if hasMetron {
			slog.Warn("backfill cover via CV failed; trying Metron",
				"series_id", seriesID, "error", cvErr)
			mu, mErr := tryMetron()
			if mErr != nil {
				return mErr
			}
			url = mu
		} else {
			return cvErr
		}
	} else if hasMetron {
		u, err := tryMetron()
		if err != nil {
			return err
		}
		url = u
	}

	if url == "" {
		return fmt.Errorf("provider returned no cover image")
	}
	return s.seriesRepo.SetSeriesCoverImageURL(seriesID, url)
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

	cvIssues, err := s.cv.GetVolumeIssues(context.Background(), cvVolumeID)
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

// MetronMatchConflictError is returned by MatchSeriesToMetronVolume when
// another local series row already owns the requested Metron series ID.
type MetronMatchConflictError struct {
	RequestedSeriesID int64
	ConflictingSeries *model.Series
	MetronID          int64
}

func (e *MetronMatchConflictError) Error() string {
	title := ""
	if e.ConflictingSeries != nil {
		title = e.ConflictingSeries.Title
	}
	return fmt.Sprintf("Metron series %d is already matched to local series %q (id=%d)",
		e.MetronID, title, e.conflictingID())
}

func (e *MetronMatchConflictError) conflictingID() int64 {
	if e.ConflictingSeries == nil {
		return 0
	}
	return e.ConflictingSeries.ID
}

// MatchSeriesToMetronVolume binds a local series to a Metron series and
// applies the metadata. If Metron carries a cv_id we also cross-link the
// ComicVine ID, so subsequent ops can pick whichever provider has quota.
func (s *MetadataService) MatchSeriesToMetronVolume(ctx context.Context, seriesID int64, metronSeriesID int) error {
	if !s.HasMetronCredentials() {
		return fmt.Errorf("Metron credentials not configured")
	}

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

	if existing, err := s.seriesRepo.FindByMetronID(int64(metronSeriesID)); err == nil && existing != nil && existing.ID != seriesID {
		return &MetronMatchConflictError{
			RequestedSeriesID: seriesID,
			ConflictingSeries: existing,
			MetronID:          int64(metronSeriesID),
		}
	}

	mSeries, lastModified, err := s.metron.GetSeriesIfModified(ctx, metronSeriesID, time.Time{})
	if err != nil {
		return fmt.Errorf("fetching Metron series: %w", err)
	}
	defer func() {
		if lastModified != "" {
			if err := s.seriesRepo.SetMetronModifiedAt(seriesID, lastModified); err != nil {
				slog.Debug("failed to save metron Last-Modified", "series_id", seriesID, "error", err)
			}
		}
	}()

	// Publisher: reuse the existing publishers table; tag with cv_id when
	// possible (Metron doesn't expose CV publisher IDs directly).
	var publisherID *int64
	if mSeries.Publisher.Name != "" {
		pub, err := s.publisherRepo.FindOrCreateByName(mSeries.Publisher.Name, nil)
		if err == nil && pub != nil {
			publisherID = &pub.ID
		}
	}

	mid := int64(mSeries.ID)
	series.MetronID = &mid
	if mSeries.CVID != nil {
		cv := int64(*mSeries.CVID)
		series.ComicVineID = &cv
	}
	series.Title = mSeries.Name
	series.SortTitle = scanner.MakeSortTitle(mSeries.Name)
	series.PublisherID = publisherID
	series.Description = mSeries.Description
	if mSeries.YearBegan > 0 {
		y := mSeries.YearBegan
		series.Year = &y
	}
	series.TotalIssues = mSeries.IssueCount
	series.Status = strings.ToLower(mSeries.Status)
	if series.Status == "" {
		series.Status = "continuing"
	}

	// Pre-flight the ux_series_norm_title_year unique index — see the matching
	// check in MatchSeriesToVolume. Without it, UpdateFromMetronMetadata leaks a
	// raw UNIQUE constraint error when another series already holds this
	// normalized title+year.
	if conflict, cErr := s.seriesRepo.FindByNormalizedTitleYear(series.Title, series.Year, seriesID); cErr == nil && conflict != nil {
		return &SeriesMatchConflictError{
			RequestedSeriesID: seriesID,
			ConflictingSeries: conflict,
		}
	}

	if err := s.seriesRepo.UpdateFromMetronMetadata(series); err != nil {
		return fmt.Errorf("updating series: %w", err)
	}

	slog.Info("matched series to Metron",
		"series_id", seriesID,
		"metron_id", metronSeriesID,
		"title", mSeries.Name,
		"cross_cv", series.ComicVineID,
	)

	if err := s.populateIssuesFromMetronSeries(ctx, series); err != nil {
		slog.Warn("failed to populate issues from Metron", "error", err)
	}
	return nil
}

// populateIssuesFromMetronSeries walks every Metron issue for the series and
// upserts local issue rows. Matches existing rows first by metron_id, then by
// comicvine_id (if Metron supplies one), then by issue_number.
func (s *MetadataService) populateIssuesFromMetronSeries(ctx context.Context, series *model.Series) error {
	if series.MetronID == nil {
		return fmt.Errorf("series has no metron_id")
	}
	mIssues, err := s.metron.GetSeriesIssues(ctx, int(*series.MetronID))
	if err != nil {
		return fmt.Errorf("fetching Metron issues: %w", err)
	}

	// Use the first issue with an image as the series cover when no cover
	// URL has been captured yet — Metron series detail doesn't carry one.
	if series.CoverImageURL == "" {
		for _, mi := range mIssues {
			if mi.Image != "" {
				if err := s.seriesRepo.SetSeriesCoverImageURL(series.ID, mi.Image); err != nil {
					slog.Debug("failed to set series cover from metron issue", "series_id", series.ID, "error", err)
				} else {
					series.CoverImageURL = mi.Image
				}
				break
			}
		}
	}

	for _, mi := range mIssues {
		// Hydrate from list; for credits / desc we'd need the detail endpoint.
		// Detail fetches are deferred — would burn quota per issue.
		issue := &model.Issue{
			SeriesID:    series.ID,
			IssueNumber: mi.Number,
			SortNumber:  scanner.SortNumber(mi.Number),
			Title:       strings.TrimSpace(mi.IssueName),
			CoverDate:   mi.CoverDate,
			StoreDate:   mi.StoreDate,
			CoverURL:    mi.Image,
			ReadStatus:  "unread",
		}
		mid := int64(mi.ID)
		issue.MetronID = &mid

		var existing *model.Issue
		if found, _ := s.issueRepo.FindByMetronID(mid); found != nil {
			existing = found
		}
		if existing == nil {
			if found, _ := s.issueRepo.FindBySeriesAndNumber(series.ID, mi.Number); found != nil {
				existing = found
			}
		}
		if existing != nil {
			issue.ID = existing.ID
			issue.ReadStatus = existing.ReadStatus
			issue.SkipStatus = existing.SkipStatus
			issue.Writers = existing.Writers
			issue.Artists = existing.Artists
			if existing.ComicVineID != nil {
				issue.ComicVineID = existing.ComicVineID
			}
			if err := s.issueRepo.UpdateFromMetronMetadata(issue); err != nil {
				slog.Warn("failed to update Metron issue", "issue_id", existing.ID, "error", err)
			}
			continue
		}
		if err := s.issueRepo.Create(issue); err != nil {
			slog.Warn("failed to create Metron issue", "series_id", series.ID, "number", mi.Number, "error", err)
			continue
		}
		// After Create the row exists with NULL metron_id (Create's INSERT
		// doesn't include metron_id); patch it on.
		if err := s.issueRepo.SetMetronID(issue.ID, &mid); err != nil {
			slog.Warn("failed to set metron_id on new issue", "issue_id", issue.ID, "error", err)
		}
	}
	return nil
}

// RefreshSeriesFromMetron re-runs the Metron match using the existing
// metron_id, picking up new issues / metadata changes. Uses If-Modified-Since
// against Metron's saved Last-Modified for the series — if the resource is
// unchanged Metron returns 304 (no quota cost) and we early-return.
func (s *MetadataService) RefreshSeriesFromMetron(ctx context.Context, seriesID int64) error {
	series, err := s.seriesRepo.GetByID(seriesID)
	if err != nil {
		return fmt.Errorf("getting series: %w", err)
	}
	if series == nil {
		return fmt.Errorf("series %d not found", seriesID)
	}
	if series.MetronID == nil {
		return fmt.Errorf("series is not matched to Metron")
	}

	var ifMod time.Time
	if cached, _ := s.seriesRepo.GetMetronModifiedAt(seriesID); cached != "" {
		if t, err := time.Parse(time.RFC1123, cached); err == nil {
			ifMod = t
		}
	}

	mSeries, lastModified, err := s.metron.GetSeriesIfModified(ctx, int(*series.MetronID), ifMod)
	if err != nil {
		if errors.Is(err, metron.ErrNotModified) {
			slog.Debug("metron series unchanged since last refresh — skipping (no quota cost)",
				"series_id", seriesID)
			return nil
		}
		return fmt.Errorf("fetching Metron series: %w", err)
	}

	// Apply metadata + issues, same path as the initial match.
	if err := s.applyMetronSeries(ctx, series, mSeries); err != nil {
		return err
	}
	if lastModified != "" {
		if err := s.seriesRepo.SetMetronModifiedAt(seriesID, lastModified); err != nil {
			slog.Debug("failed to save metron Last-Modified", "series_id", seriesID, "error", err)
		}
	}
	return nil
}

// applyMetronSeries is the body extracted from MatchSeriesToMetronVolume so
// RefreshSeriesFromMetron can reuse it with a pre-fetched Series payload (the
// conditional-GET path doesn't want to re-fetch).
func (s *MetadataService) applyMetronSeries(ctx context.Context, series *model.Series, mSeries *metron.Series) error {
	var publisherID *int64
	if mSeries.Publisher.Name != "" {
		pub, err := s.publisherRepo.FindOrCreateByName(mSeries.Publisher.Name, nil)
		if err == nil && pub != nil {
			publisherID = &pub.ID
		}
	}

	mid := int64(mSeries.ID)
	series.MetronID = &mid
	if mSeries.CVID != nil {
		cv := int64(*mSeries.CVID)
		series.ComicVineID = &cv
	}
	series.Title = mSeries.Name
	series.SortTitle = scanner.MakeSortTitle(mSeries.Name)
	series.PublisherID = publisherID
	series.Description = mSeries.Description
	if mSeries.YearBegan > 0 {
		y := mSeries.YearBegan
		series.Year = &y
	}
	series.TotalIssues = mSeries.IssueCount
	series.Status = strings.ToLower(mSeries.Status)
	if series.Status == "" {
		series.Status = "continuing"
	}

	if err := s.seriesRepo.UpdateFromMetronMetadata(series); err != nil {
		return fmt.Errorf("updating series: %w", err)
	}
	if err := s.populateIssuesFromMetronSeries(ctx, series); err != nil {
		slog.Warn("failed to populate issues from Metron", "error", err)
	}
	return nil
}

// CrossLinkSeriesToMetron looks up the Metron equivalent of an already-CV-matched
// series via Metron's ?cv_id= filter. Stores the metron_id without touching
// other fields. Best-effort — silently no-ops if no Metron mapping exists.
func (s *MetadataService) CrossLinkSeriesToMetron(ctx context.Context, seriesID int64) error {
	if !s.HasMetronCredentials() {
		return nil
	}
	series, err := s.seriesRepo.GetByID(seriesID)
	if err != nil || series == nil || series.ComicVineID == nil || series.MetronID != nil {
		return nil
	}
	hit, err := s.metron.SeriesByCVID(ctx, int(*series.ComicVineID))
	if err != nil || hit == nil {
		return nil
	}
	mid := int64(hit.ID)
	if err := s.seriesRepo.SetMetronID(seriesID, &mid); err != nil {
		slog.Warn("cross-link metron_id failed", "series_id", seriesID, "error", err)
		return err
	}
	slog.Info("cross-linked series to Metron via cv_id",
		"series_id", seriesID, "cv_id", *series.ComicVineID, "metron_id", mid)
	return nil
}

// SearchMetronSeries searches the Metron API for a series matching the query.
// Mirrors the shape the UI already consumes for ComicVine results.
type MetronSearchResult struct {
	MetronID    int    `json:"metron_id"`
	Name        string `json:"name"`
	YearBegan   int    `json:"year_began"`
	IssueCount  int    `json:"issue_count"`
	Volume      int    `json:"volume"`
	DisplayName string `json:"display_name"`
}

// SearchMetron returns search results from the Metron series endpoint.
func (s *MetadataService) SearchMetron(ctx context.Context, query string, page int) ([]MetronSearchResult, int, error) {
	if !s.HasMetronCredentials() {
		return nil, 0, fmt.Errorf("Metron credentials not configured")
	}
	if page < 1 {
		page = 1
	}
	params := url.Values{}
	params.Set("name", query)
	params.Set("page", strconv.Itoa(page))
	resp, err := s.metron.SearchSeries(ctx, params)
	if err != nil {
		return nil, 0, err
	}
	out := make([]MetronSearchResult, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, MetronSearchResult{
			MetronID:    r.ID,
			Name:        r.Display,
			DisplayName: r.Display,
			YearBegan:   r.YearBegan,
			IssueCount:  r.IssueCount,
			Volume:      r.Volume,
		})
	}
	return out, resp.Count, nil
}

// CVMatchConflictError is returned by MatchSeriesToVolume when another local
// series row already owns the requested ComicVine volume ID. Lets the caller
// surface a merge-or-unmatch flow instead of leaking a UNIQUE constraint
// failure to the user.
type CVMatchConflictError struct {
	RequestedSeriesID int64
	ConflictingSeries *model.Series
	ComicVineID       int
}

func (e *CVMatchConflictError) Error() string {
	title := ""
	if e.ConflictingSeries != nil {
		title = e.ConflictingSeries.Title
	}
	return fmt.Sprintf("ComicVine volume %d is already matched to series %q (id=%d)", e.ComicVineID, title, e.conflictingID())
}

func (e *CVMatchConflictError) conflictingID() int64 {
	if e.ConflictingSeries == nil {
		return 0
	}
	return e.ConflictingSeries.ID
}

// SeriesMatchConflictError is returned when applying a match would collide
// with an existing local series under the ux_series_norm_title_year unique
// index — i.e. another series already has the same normalized title+year the
// match would assign. Without this pre-flight, UpdateFromMetadata leaks a raw
// "UNIQUE constraint failed" error to the user. The resolution is a merge of
// the two series.
type SeriesMatchConflictError struct {
	RequestedSeriesID int64
	ConflictingSeries *model.Series
}

func (e *SeriesMatchConflictError) Error() string {
	title := ""
	var id int64
	if e.ConflictingSeries != nil {
		title = e.ConflictingSeries.Title
		id = e.ConflictingSeries.ID
	}
	return fmt.Sprintf("a series titled %q already exists (id=%d) — merge required", title, id)
}

// MatchSeriesToVolume matches a local series to a ComicVine volume and applies metadata.
func (s *MetadataService) MatchSeriesToVolume(ctx context.Context, seriesID int64, cvVolumeID int) error {
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

	// If another local series row already owns this CV ID, surface a typed
	// conflict so callers can offer a merge instead of failing on the UNIQUE
	// constraint at UPDATE time.
	if existing, err := s.seriesRepo.FindByComicVineID(int64(cvVolumeID)); err == nil && existing != nil && existing.ID != seriesID {
		return &CVMatchConflictError{
			RequestedSeriesID: seriesID,
			ConflictingSeries: existing,
			ComicVineID:       cvVolumeID,
		}
	}

	// Fetch volume from ComicVine
	volume, err := s.cv.GetVolume(ctx, cvVolumeID)
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
	if volume.Image != nil {
		series.CoverImageURL = bestImageURL(volume.Image)
	}

	if volume.StartYear != "" {
		var year int
		fmt.Sscanf(volume.StartYear, "%d", &year)
		if year > 0 {
			series.Year = &year
		}
	}

	// Determine status
	series.Status = "continuing"

	// Pre-flight the ux_series_norm_title_year unique index. If another local
	// series already holds this normalized title+year, UpdateFromMetadata would
	// fail with a raw "UNIQUE constraint failed" error. Surface a typed conflict
	// so the caller can offer a merge instead.
	if conflict, cErr := s.seriesRepo.FindByNormalizedTitleYear(series.Title, series.Year, seriesID); cErr == nil && conflict != nil {
		return &SeriesMatchConflictError{
			RequestedSeriesID: seriesID,
			ConflictingSeries: conflict,
		}
	}

	if err := s.seriesRepo.UpdateFromMetadata(series); err != nil {
		return fmt.Errorf("updating series: %w", err)
	}

	slog.Info("matched series to ComicVine volume",
		"series_id", seriesID,
		"cv_volume_id", cvVolumeID,
		"title", volume.Name,
	)

	// Now fetch all issues for this volume and populate missing issues
	if err := s.populateIssuesFromVolume(ctx, series, volume); err != nil {
		slog.Warn("failed to populate issues from volume", "error", err)
	}

	// Best-effort cross-link to Metron via ?cv_id= so future ops can route
	// to whichever provider has quota left. Failure is silent.
	if s.HasMetronCredentials() {
		go func(sID int64) {
			bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = s.CrossLinkSeriesToMetron(bg, sID)
		}(seriesID)
	}

	return nil
}

// populateIssuesFromVolume creates issue records for any issues from the
// ComicVine volume that don't exist locally yet, and updates existing ones.
func (s *MetadataService) populateIssuesFromVolume(ctx context.Context, series *model.Series, volume *comicvine.Volume) error {
	// Get all issues from ComicVine for this volume
	cvIssues, err := s.cv.GetVolumeIssues(ctx, volume.ID)
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

	cvIssue, err := s.cv.GetIssue(context.Background(), cvIssueID)
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

// RefreshSeriesAuto picks the best metadata provider for a series and refreshes
// from it. Decision tree:
//   - If only one provider is matched, use that one.
//   - If both are matched, prefer whichever has more remaining quota
//     (Metron's burst counter vs CV's hourly counter, normalized).
//   - If neither is matched, return an error.
//
// Quota-aware fallback: if the chosen provider returns a quota / 429 / context
// error, automatically retry against the other provider when that one is
// matched too. Errors from the alternate provider are returned verbatim.
func (s *MetadataService) RefreshSeriesAuto(ctx context.Context, seriesID int64) error {
	series, err := s.seriesRepo.GetByID(seriesID)
	if err != nil {
		return fmt.Errorf("getting series: %w", err)
	}
	if series == nil {
		return fmt.Errorf("series %d not found", seriesID)
	}
	hasCV := series.ComicVineID != nil
	hasMetron := series.MetronID != nil && s.HasMetronCredentials()

	if !hasCV && !hasMetron {
		return fmt.Errorf("series is not matched to any metadata provider")
	}

	primary, fallback := s.pickProvider(hasCV, hasMetron)

	if err := s.refreshVia(ctx, seriesID, primary); err != nil {
		// Honor cancel
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if fallback != "" && fallback != primary {
			slog.Warn("primary provider failed, falling back",
				"series_id", seriesID, "primary", primary, "fallback", fallback, "error", err)
			return s.refreshVia(ctx, seriesID, fallback)
		}
		return err
	}
	return nil
}

func (s *MetadataService) refreshVia(ctx context.Context, seriesID int64, provider string) error {
	switch provider {
	case "metron":
		return s.RefreshSeriesFromMetron(ctx, seriesID)
	case "comicvine":
		return s.RefreshSeriesMetadata(ctx, seriesID)
	}
	return fmt.Errorf("unknown provider %q", provider)
}

// pickProvider returns (primary, fallback). Picks Metron when CV is exhausted
// or both are configured and Metron has more headroom. Empty fallback means
// no alternative is matched.
func (s *MetadataService) pickProvider(hasCV, hasMetron bool) (string, string) {
	switch {
	case hasCV && !hasMetron:
		return "comicvine", ""
	case hasMetron && !hasCV:
		return "metron", ""
	case hasCV && hasMetron:
		cvLeft := s.cv.HourlyRemaining()
		mq := s.metron.Quota()
		// CV: 200/hr, Metron: 5000/day burst-gated by 20/min. Normalize as
		// "calls available right now": CV uses HourlyRemaining as-is, Metron
		// uses min(burst, sustained). When CV is at 0 we always prefer Metron;
		// when Metron is at 0 we prefer CV.
		mLeft := mq.SustainedRemaining
		if mq.BurstRemaining > 0 && mq.BurstRemaining < mLeft {
			mLeft = mq.BurstRemaining
		}
		if cvLeft <= 0 && mLeft > 0 {
			return "metron", "comicvine"
		}
		if mLeft <= 0 && cvLeft > 0 {
			return "comicvine", "metron"
		}
		// Both have headroom. Prefer Metron for its larger ceiling and
		// per-minute window — CV is cheaper to recover but easier to drain.
		if mLeft >= cvLeft {
			return "metron", "comicvine"
		}
		return "comicvine", "metron"
	}
	return "", ""
}

// RefreshSeriesMetadata re-fetches metadata for a series that's already matched.
func (s *MetadataService) RefreshSeriesMetadata(ctx context.Context, seriesID int64) error {
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

	return s.MatchSeriesToVolume(ctx, seriesID, int(*series.ComicVineID))
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

		// If the hourly CV quota is exhausted, surface that explicitly so
		// the user sees in the Jobs UI that we're waiting (not stuck), and
		// can hit Cancel to abort. The next CV call will block in the
		// limiter — interruptible — until the window resets.
		if remaining := s.cv.HourlyRemaining(); remaining == 0 {
			eta := s.cv.NextResetIn().Round(time.Minute)
			if eta < time.Minute {
				eta = time.Minute
			}
			if progress != nil {
				progress(i, total, fmt.Sprintf("ComicVine quota exhausted — waiting %s for reset", eta))
			}
		} else if progress != nil {
			progress(i, total, fmt.Sprintf("Refreshing %s (CV quota: %d remaining)", series.Title, remaining))
		}

		if series.ComicVineID == nil {
			slog.Debug("skipping unmatched tracked series", "id", series.ID, "title", series.Title)
			continue
		}

		if err := s.RefreshSeriesAuto(ctx, series.ID); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return refreshed, failed, err
			}
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
//
// ctx bounds both the walksoftly call and the CV fallback. Critical when
// a library scan is mid-run holding the CV rate limiter — without a
// cancellable ctx, this handler blocks until the limiter wakes up
// (potentially the full hourly reset window) and the calling page
// stays "Loading…" forever.
func (s *MetadataService) GetWeeklyReleases(ctx context.Context, startDate, endDate string) ([]PullListIssue, *ReleaseDebugInfo, error) {
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
		wsReleases, wsErr := s.ws.GetWeeklyReleasesCtx(ctx, weekNum, weekYear)
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
		cvResults, cvCount := s.buildResultsFromComicVine(ctx, startDate, endDate, localByCV, trackedSeriesByCV, localSeriesByCV, wantedIssueIDs)
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
			// Publisher fallback: walksoftly sometimes ships releases with an
			// empty publisher, which dumps them into the UI's "Other" bucket.
			// When the series exists locally (by CV ID) or was cached by a
			// prior CV pass, use that publisher instead.
			if item.Publisher == "" {
				if ls, ok := localSeriesByCV[seriesCVID]; ok && ls.PublisherName != "" {
					item.Publisher = ls.PublisherName
				} else if cached, ok := s.publisherCache[int(seriesCVID)]; ok {
					item.Publisher = cached
				}
			}
		}

		results = append(results, item)
	}

	return results
}

// buildResultsFromComicVine is the fallback path using ComicVine's store_date API.
func (s *MetadataService) buildResultsFromComicVine(
	ctx context.Context,
	startDate, endDate string,
	localByCV map[int64]*model.Issue,
	trackedSeriesByCV map[int64]*model.Series,
	localSeriesByCV map[int64]*model.Series,
	wantedIssueIDs map[int64]bool,
) ([]PullListIssue, int) {
	cvIssues, err := s.cv.GetIssuesByStoreDate(ctx, startDate, endDate)
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
		volumes, err := s.cv.GetVolumesByIDs(context.Background(), uniqueUncached)
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
		volume, err := s.cv.GetVolume(context.Background(), cvVolumeID)
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

		// Pre-flight ux_series_norm_title_year before INSERT. If a local series
		// already holds this normalized title+year under a different CV ID, the
		// Create below blows up with a raw "UNIQUE constraint failed" error.
		// Surface a typed conflict so writeMatchConflict returns 409 MERGE_REQUIRED
		// and the UI can offer "Go to <existing>" instead of leaking SQL.
		if conflict, cErr := s.seriesRepo.FindByNormalizedTitleYear(series.Title, series.Year, 0); cErr == nil && conflict != nil {
			return nil, 0, &SeriesMatchConflictError{
				ConflictingSeries: conflict,
			}
		}

		if err := s.seriesRepo.Create(series); err != nil {
			return nil, 0, fmt.Errorf("creating series: %w", err)
		}

		// Step 4: Match the series to the ComicVine volume
		// (this updates series metadata and populates all issues)
		if err := s.MatchSeriesToVolume(context.Background(), series.ID, cvVolumeID); err != nil {
			return nil, 0, fmt.Errorf("matching series to volume: %w", err)
		}
	}

	// Step 5: Mark the series as tracked
	if err := s.seriesRepo.SetTracked(series.ID, true); err != nil {
		return nil, 0, fmt.Errorf("tracking series: %w", err)
	}

	// Step 6: Add missing issues to the want list ONLY if the caller explicitly
	// asks for it. Default is off — tracking a series should not silently dump
	// every back issue ever published into the want list. (e.g., tracking
	// "Zorro" once accidentally enqueued 30 issues from a 1952 reprint series.)
	wantAdded := 0
	shouldWantAll := len(wantAll) > 0 && wantAll[0]
	if wantListRepo != nil && shouldWantAll {
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

	// Metron credentials
	settings["metron_username"] = s.MetronUsername()
	settings["metron_token_masked"] = s.MetronTokenMasked()
	settings["metron_token_set"] = s.MetronTokenSet()
	mq := s.MetronQuota()
	settings["metron_burst_remaining"] = mq.BurstRemaining
	settings["metron_sustained_remaining"] = mq.SustainedRemaining

	// Scan reconciliation settings
	scanAutoQueue, _ := s.settingRepo.Get("scan_auto_queue_backlog")
	settings["scan_auto_queue_backlog"] = scanAutoQueue == "true"

	scanCVTTLStr, _ := s.settingRepo.Get("scan_cv_refresh_ttl_hours")
	scanCVTTL := 24
	if v, err := strconv.Atoi(scanCVTTLStr); err == nil && v > 0 {
		scanCVTTL = v
	}
	settings["scan_cv_refresh_ttl_hours"] = scanCVTTL

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

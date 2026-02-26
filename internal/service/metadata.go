package service

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jeremy/longbox/internal/comicvine"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scanner"
)

// MetadataService handles ComicVine metadata operations.
type MetadataService struct {
	cv               *comicvine.Client
	seriesRepo       *repository.SeriesRepo
	issueRepo        *repository.IssueRepo
	publisherRepo    *repository.PublisherRepo
	settingRepo      *repository.SettingRepo
	configAPIKey     string
	configLibraryDir string
}

func NewMetadataService(
	cv *comicvine.Client,
	seriesRepo *repository.SeriesRepo,
	issueRepo *repository.IssueRepo,
	publisherRepo *repository.PublisherRepo,
	settingRepo *repository.SettingRepo,
	configAPIKey string,
	configLibraryDir string,
) *MetadataService {
	return &MetadataService{
		cv:               cv,
		seriesRepo:       seriesRepo,
		issueRepo:        issueRepo,
		publisherRepo:    publisherRepo,
		settingRepo:      settingRepo,
		configAPIKey:     configAPIKey,
		configLibraryDir: configLibraryDir,
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

	return settings
}

// timestamp helper
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

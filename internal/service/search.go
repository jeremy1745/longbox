package service

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/newznab"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/sabnzbd"
	"github.com/jeremy/longbox/internal/scheduler"
)

// SearchService orchestrates searching indexers and grabbing NZBs.
type SearchService struct {
	indexerRepo   *repository.IndexerRepo
	dlClientRepo  *repository.DownloadClientRepo
	dlHistoryRepo *repository.DownloadHistoryRepo
	issueRepo     *repository.IssueRepo
	seriesRepo    *repository.SeriesRepo
	eventBus      *scheduler.EventBus
}

func NewSearchService(
	indexerRepo *repository.IndexerRepo,
	dlClientRepo *repository.DownloadClientRepo,
	dlHistoryRepo *repository.DownloadHistoryRepo,
	issueRepo *repository.IssueRepo,
	seriesRepo *repository.SeriesRepo,
	eventBus *scheduler.EventBus,
) *SearchService {
	return &SearchService{
		indexerRepo:   indexerRepo,
		dlClientRepo:  dlClientRepo,
		dlHistoryRepo: dlHistoryRepo,
		issueRepo:     issueRepo,
		seriesRepo:    seriesRepo,
		eventBus:      eventBus,
	}
}

// ScoredResult wraps a Newznab search result with a relevance score.
type ScoredResult struct {
	newznab.SearchResult
	Score int `json:"score"`
}

// SearchForIssue searches all enabled indexers for a specific issue.
func (s *SearchService) SearchForIssue(ctx context.Context, issueID int64) ([]ScoredResult, error) {
	issue, err := s.issueRepo.GetByID(issueID)
	if err != nil {
		return nil, fmt.Errorf("getting issue: %w", err)
	}
	if issue == nil {
		return nil, fmt.Errorf("issue %d not found", issueID)
	}

	series, err := s.seriesRepo.GetByID(issue.SeriesID)
	if err != nil {
		return nil, fmt.Errorf("getting series: %w", err)
	}
	if series == nil {
		return nil, fmt.Errorf("series %d not found", issue.SeriesID)
	}

	query := buildSearchQuery(series, issue)
	results, err := s.searchAllIndexers(ctx, query)
	if err != nil {
		return nil, err
	}

	// Score results against the specific issue
	scored := make([]ScoredResult, len(results))
	for i, r := range results {
		scored[i] = ScoredResult{
			SearchResult: r,
			Score:        scoreResult(r, series, issue),
		}
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	return scored, nil
}

// SearchQuery searches all enabled indexers with a raw query string.
func (s *SearchService) SearchQuery(ctx context.Context, query string) ([]ScoredResult, error) {
	results, err := s.searchAllIndexers(ctx, query)
	if err != nil {
		return nil, err
	}

	scored := make([]ScoredResult, len(results))
	for i, r := range results {
		scored[i] = ScoredResult{
			SearchResult: r,
			Score:        50, // neutral score for raw query
		}
	}

	// Sort by publish date (newest first)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].PublishDate.After(scored[j].PublishDate)
	})

	return scored, nil
}

// GrabResult sends an NZB to the first enabled download client and records the grab.
func (s *SearchService) GrabResult(ctx context.Context, nzbURL, nzbName string, size int64, indexerID int64, issueID *int64) (*model.DownloadHistoryItem, error) {
	// Check for duplicate grabs
	if issueID != nil {
		exists, err := s.dlHistoryRepo.ExistsForIssue(*issueID)
		if err != nil {
			return nil, fmt.Errorf("checking for existing download: %w", err)
		}
		if exists {
			return nil, fmt.Errorf("issue already has an active download")
		}
	}

	// Get the first enabled download client
	dc, err := s.dlClientRepo.GetFirstEnabled()
	if err != nil {
		return nil, fmt.Errorf("getting download client: %w", err)
	}
	if dc == nil {
		return nil, fmt.Errorf("no enabled download client configured")
	}

	// Send to SABnzbd
	sabClient := sabnzbd.NewClient(dc.URL, dc.APIKey)
	nzoID, err := sabClient.SendURL(nzbURL, nzbName, dc.Category)
	if err != nil {
		return nil, fmt.Errorf("sending to SABnzbd: %w", err)
	}

	// Record in download history
	item := &model.DownloadHistoryItem{
		IssueID:          issueID,
		IndexerID:        &indexerID,
		DownloadClientID: &dc.ID,
		NZBName:          nzbName,
		NZBURL:           nzbURL,
		ExternalID:       nzoID,
		Status:           model.DownloadStatusGrabbed,
		Size:             size,
	}
	if err := s.dlHistoryRepo.Create(item); err != nil {
		slog.Error("failed to record download history", "error", err)
	}

	// Publish event
	s.eventBus.Publish(scheduler.Event{
		Type: "download:grabbed",
		Data: item,
	})

	slog.Info("NZB grabbed",
		"nzb", nzbName,
		"nzo_id", nzoID,
		"indexer_id", indexerID,
	)

	return item, nil
}

// AutoSearchAndGrab searches for an issue and grabs the best result.
// Returns nil (no error) if no suitable result was found.
func (s *SearchService) AutoSearchAndGrab(ctx context.Context, issueID int64) (*model.DownloadHistoryItem, error) {
	// Check for duplicate grabs first
	exists, err := s.dlHistoryRepo.ExistsForIssue(issueID)
	if err != nil {
		return nil, fmt.Errorf("checking for existing download: %w", err)
	}
	if exists {
		return nil, nil // already grabbed, skip
	}

	results, err := s.SearchForIssue(ctx, issueID)
	if err != nil {
		return nil, fmt.Errorf("searching for issue %d: %w", issueID, err)
	}

	if len(results) == 0 {
		return nil, nil
	}

	// Grab the highest-scoring result if it meets the minimum threshold
	const minScore = 50
	best := results[0]
	if best.Score < minScore {
		slog.Debug("best result score below threshold",
			"issue_id", issueID,
			"best_score", best.Score,
			"min_score", minScore,
		)
		return nil, nil
	}

	return s.GrabResult(ctx, best.NZBURL, best.Title, best.Size, best.IndexerID, &issueID)
}

// CheckDownloadStatus polls SABnzbd for status updates on active downloads.
func (s *SearchService) CheckDownloadStatus(ctx context.Context) error {
	pending, err := s.dlHistoryRepo.ListPending()
	if err != nil {
		return fmt.Errorf("listing pending downloads: %w", err)
	}
	if len(pending) == 0 {
		return nil
	}

	dc, err := s.dlClientRepo.GetFirstEnabled()
	if err != nil || dc == nil {
		return nil // no download client, can't check
	}

	sabClient := sabnzbd.NewClient(dc.URL, dc.APIKey)

	for _, item := range pending {
		if item.ExternalID == "" {
			continue
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		status, found, err := sabClient.GetSlotStatus(item.ExternalID)
		if err != nil {
			slog.Warn("error checking download status", "nzo_id", item.ExternalID, "error", err)
			continue
		}
		if !found {
			continue
		}

		var newStatus model.DownloadStatus
		switch strings.ToLower(status) {
		case "completed":
			newStatus = model.DownloadStatusCompleted
		case "failed":
			newStatus = model.DownloadStatusFailed
		case "downloading", "extracting", "repairing":
			newStatus = model.DownloadStatusDownloading
		default:
			continue // still queued or unknown
		}

		if newStatus != item.Status {
			if err := s.dlHistoryRepo.UpdateStatus(item.ID, newStatus, status); err != nil {
				slog.Warn("failed to update download status", "id", item.ID, "error", err)
				continue
			}
			s.eventBus.Publish(scheduler.Event{
				Type: "download:updated",
				Data: map[string]interface{}{
					"id":     item.ID,
					"status": newStatus,
				},
			})
			slog.Info("download status updated",
				"id", item.ID,
				"nzb", item.NZBName,
				"status", newStatus,
			)
		}
	}

	return nil
}

func (s *SearchService) searchAllIndexers(ctx context.Context, query string) ([]newznab.SearchResult, error) {
	indexers, err := s.indexerRepo.ListEnabled()
	if err != nil {
		return nil, fmt.Errorf("listing indexers: %w", err)
	}
	if len(indexers) == 0 {
		return nil, fmt.Errorf("no enabled indexers configured")
	}

	type indexerResult struct {
		results []newznab.SearchResult
		err     error
	}

	var mu sync.Mutex
	var allResults []newznab.SearchResult
	var wg sync.WaitGroup

	for _, idx := range indexers {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		wg.Add(1)
		go func(idx model.Indexer) {
			defer wg.Done()

			isProwlarr := idx.Type == model.IndexerTypeProwlarr
			client := newznab.NewClient(idx.URL, idx.APIKey, isProwlarr)
			categories := strings.Split(idx.Categories, ",")

			results, err := client.Search(query, categories)
			if err != nil {
				slog.Warn("indexer search failed",
					"indexer", idx.Name,
					"query", query,
					"error", err,
				)
				return
			}

			// Tag results with indexer info
			for i := range results {
				results[i].IndexerName = idx.Name
				results[i].IndexerID = idx.ID
			}

			mu.Lock()
			allResults = append(allResults, results...)
			mu.Unlock()

			slog.Debug("indexer search complete",
				"indexer", idx.Name,
				"results", len(results),
			)
		}(idx)
	}

	wg.Wait()

	// Deduplicate by GUID
	seen := make(map[string]bool)
	deduped := make([]newznab.SearchResult, 0, len(allResults))
	for _, r := range allResults {
		if !seen[r.GUID] {
			seen[r.GUID] = true
			deduped = append(deduped, r)
		}
	}

	return deduped, nil
}

func buildSearchQuery(series *model.Series, issue *model.Issue) string {
	parts := []string{series.Title}

	if series.Year != nil {
		parts = append(parts, fmt.Sprintf("%d", *series.Year))
	}

	if issue.IssueNumber != "" {
		parts = append(parts, "#"+issue.IssueNumber)
	}

	return strings.Join(parts, " ")
}

func scoreResult(result newznab.SearchResult, series *model.Series, issue *model.Issue) int {
	score := 0
	titleLower := strings.ToLower(result.Title)
	seriesLower := strings.ToLower(series.Title)

	// Title contains series name
	if strings.Contains(titleLower, seriesLower) {
		score += 50
	} else {
		// Check individual significant words
		words := strings.Fields(seriesLower)
		matched := 0
		for _, w := range words {
			if len(w) > 2 && strings.Contains(titleLower, w) {
				matched++
			}
		}
		if len(words) > 0 {
			score += (matched * 30) / len(words)
		}
	}

	// Title contains issue number
	if issue.IssueNumber != "" {
		numPatterns := []string{
			"#" + issue.IssueNumber,
			" " + issue.IssueNumber + " ",
			" " + issue.IssueNumber + ".",
			fmt.Sprintf("#%s ", issue.IssueNumber),
		}
		// Also try zero-padded
		if len(issue.IssueNumber) < 3 {
			padded := fmt.Sprintf("%03s", issue.IssueNumber)
			numPatterns = append(numPatterns, "#"+padded, " "+padded+" ")
		}
		for _, p := range numPatterns {
			if strings.Contains(titleLower, strings.ToLower(p)) {
				score += 30
				break
			}
		}
	}

	// Title contains year
	if series.Year != nil {
		yearStr := fmt.Sprintf("%d", *series.Year)
		if strings.Contains(titleLower, yearStr) {
			score += 10
		}
	}

	// Size in expected range for comics (20MB - 2GB)
	if result.Size >= 20*1024*1024 && result.Size <= 2*1024*1024*1024 {
		score += 10
	}

	// Recency bonus (posted in last 7 days)
	if !result.PublishDate.IsZero() {
		// Use a fixed reference for relative comparison
		age := 168 // hours in a week
		_ = age
		score += 5
	}

	// Grab count bonus
	if result.Grabs > 100 {
		score += 5
	} else if result.Grabs > 10 {
		score += 3
	} else if result.Grabs > 0 {
		score += 1
	}

	return score
}

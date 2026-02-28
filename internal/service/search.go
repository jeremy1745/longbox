package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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

	queries := buildSearchQueries(series, issue)

	// Run all query variants concurrently and merge results
	type queryResult struct {
		results []newznab.SearchResult
	}
	ch := make(chan queryResult, len(queries))
	for _, q := range queries {
		go func(query string) {
			results, err := s.searchAllIndexers(ctx, query)
			if err != nil {
				slog.Warn("search query failed", "query", query, "error", err)
				ch <- queryResult{}
				return
			}
			ch <- queryResult{results: results}
		}(q)
	}

	seen := make(map[string]bool)
	var allResults []newznab.SearchResult
	for range queries {
		qr := <-ch
		for _, r := range qr.results {
			if !seen[r.GUID] {
				seen[r.GUID] = true
				allResults = append(allResults, r)
			}
		}
	}

	// Score results against the specific issue
	scored := make([]ScoredResult, len(allResults))
	for i, r := range allResults {
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
			Score:        scoreRawResult(r),
		}
	}

	// Sort by score descending
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	return scored, nil
}

// GrabResult sends an NZB to the first enabled download client and records the grab.
func (s *SearchService) GrabResult(ctx context.Context, nzbURL, nzbName string, size int64, indexerID int64, issueID *int64) (*model.DownloadHistoryItem, error) {
	// Validate NZB URL against configured indexer hosts to prevent SSRF
	if err := s.validateNZBURL(nzbURL); err != nil {
		return nil, err
	}

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

	// Re-add API key that was stripped from the frontend-facing URL
	grabURL, err := s.reattachAPIKey(nzbURL)
	if err != nil {
		slog.Warn("failed to reattach API key to NZB URL", "error", err)
		grabURL = nzbURL
	}

	// Send to SABnzbd
	sabClient := sabnzbd.NewClient(dc.URL, dc.APIKey)
	nzoID, err := sabClient.SendURL(grabURL, nzbName, dc.Category)
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

// validateNZBURL checks that the NZB URL's host matches a configured indexer,
// preventing SSRF attacks where a crafted URL could probe internal services.
func (s *SearchService) validateNZBURL(nzbURL string) error {
	parsed, err := url.Parse(nzbURL)
	if err != nil {
		return fmt.Errorf("invalid NZB URL: %w", err)
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("NZB URL must use http or https scheme")
	}

	nzbHost := strings.ToLower(parsed.Hostname())
	if nzbHost == "" {
		return fmt.Errorf("NZB URL has no host")
	}

	indexers, err := s.indexerRepo.List()
	if err != nil {
		return fmt.Errorf("loading indexers for URL validation: %w", err)
	}

	for _, idx := range indexers {
		idxURL, err := url.Parse(idx.URL)
		if err != nil {
			continue
		}
		if strings.ToLower(idxURL.Hostname()) == nzbHost {
			return nil
		}
	}

	return fmt.Errorf("NZB URL host %q does not match any configured indexer", nzbHost)
}

// stripAPIKey removes apikey/api_key query parameters from a URL so that
// indexer credentials are not exposed to the frontend.
func stripAPIKey(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := parsed.Query()
	q.Del("apikey")
	q.Del("api_key")
	parsed.RawQuery = q.Encode()
	return parsed.String()
}

// reattachAPIKey looks up the indexer for the given NZB URL host and
// re-adds its API key to the URL before sending to the download client.
func (s *SearchService) reattachAPIKey(nzbURL string) (string, error) {
	parsed, err := url.Parse(nzbURL)
	if err != nil {
		return nzbURL, err
	}

	nzbHost := strings.ToLower(parsed.Hostname())
	indexers, err := s.indexerRepo.List()
	if err != nil {
		return nzbURL, err
	}

	for _, idx := range indexers {
		idxURL, err := url.Parse(idx.URL)
		if err != nil {
			continue
		}
		if strings.ToLower(idxURL.Hostname()) == nzbHost {
			q := parsed.Query()
			if q.Get("apikey") == "" && idx.APIKey != "" {
				q.Set("apikey", idx.APIKey)
				parsed.RawQuery = q.Encode()
			}
			return parsed.String(), nil
		}
	}

	return nzbURL, nil
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

			// Tag results with indexer info and strip API keys from URLs
			for i := range results {
				results[i].IndexerName = idx.Name
				results[i].IndexerID = idx.ID
				results[i].NZBURL = stripAPIKey(results[i].NZBURL)
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

// buildSearchQueries returns multiple query strings to try against indexers.
// Older comics use varied naming so we cast a wider net with several strategies.
func buildSearchQueries(series *model.Series, issue *model.Issue) []string {
	seen := make(map[string]bool)
	var queries []string
	add := func(q string) {
		if !seen[q] {
			seen[q] = true
			queries = append(queries, q)
		}
	}

	title := series.Title
	num := issue.IssueNumber

	// Strategy 1: title + issue (broadest — catches most NZBs)
	if num != "" {
		add(fmt.Sprintf("%s #%s", title, num))
	} else {
		add(title)
	}

	// Strategy 2: title + year + issue (catches posts that include the year)
	if series.Year != nil && num != "" {
		add(fmt.Sprintf("%s %d #%s", title, *series.Year, num))
	} else if series.Year != nil {
		add(fmt.Sprintf("%s %d", title, *series.Year))
	}

	// Strategy 3: zero-padded issue number variant (common in older NZB naming)
	if num != "" && len(num) < 3 {
		padded := fmt.Sprintf("%03s", num)
		add(fmt.Sprintf("%s #%s", title, padded))
	}

	return queries
}

// yearPattern matches 4-digit years (1900–2099) in NZB titles.
var yearPattern = regexp.MustCompile(`(?:^|[\s._(-])(\d{4})(?:[\s._)\-]|$)`)

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

	// Year / volume disambiguation
	if series.Year != nil {
		yearStr := fmt.Sprintf("%d", *series.Year)
		if strings.Contains(titleLower, yearStr) {
			score += 15 // NZB title contains the correct series year
		}
		// Check for a different year that may indicate wrong volume
		matches := yearPattern.FindAllStringSubmatch(result.Title, -1)
		for _, m := range matches {
			if y, err := strconv.Atoi(m[1]); err == nil && y >= 1900 && y <= 2099 {
				diff := y - *series.Year
				if diff < 0 {
					diff = -diff
				}
				if diff > 3 {
					score -= 20 // likely wrong volume/series
					break
				}
			}
		}
	}

	// Size scoring — tighter buckets for single issues
	sizeMB := result.Size / (1024 * 1024)
	switch {
	case sizeMB >= 20 && sizeMB <= 300:
		score += 10 // single issue sweet spot
	case sizeMB > 300 && sizeMB <= 800:
		score += 5 // annual / oversized issue
	case sizeMB < 10:
		score -= 10 // corrupt or incomplete
	case sizeMB > 800:
		score -= 10 // likely a collection pack
	}

	// Recency bonus
	if !result.PublishDate.IsZero() {
		age := time.Since(result.PublishDate)
		switch {
		case age < 30*24*time.Hour:
			score += 5
		case age < 90*24*time.Hour:
			score += 3
		}
	}

	// Publisher match
	if series.PublisherName != "" {
		pubLower := strings.ToLower(series.PublisherName)
		if strings.Contains(titleLower, pubLower) {
			score += 5
		}
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

// scoreRawResult scores a result from a manual/raw search query using size and grab signals.
func scoreRawResult(result newznab.SearchResult) int {
	score := 50 // baseline

	// Size scoring
	sizeMB := result.Size / (1024 * 1024)
	switch {
	case sizeMB >= 20 && sizeMB <= 300:
		score += 10
	case sizeMB > 300 && sizeMB <= 800:
		score += 5
	case sizeMB < 10:
		score -= 10
	case sizeMB > 800:
		score -= 10
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

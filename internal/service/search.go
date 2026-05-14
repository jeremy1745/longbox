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
	"unicode"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/newznab"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/sabnzbd"
	"github.com/jeremy/longbox/internal/scheduler"
)

// SearchService orchestrates searching indexers and grabbing NZBs.
type SearchService struct {
	indexerRepo             *repository.IndexerRepo
	dlClientRepo            *repository.DownloadClientRepo
	dlHistoryRepo           *repository.DownloadHistoryRepo
	issueRepo               *repository.IssueRepo
	seriesRepo              *repository.SeriesRepo
	blocklistRepo           *repository.BlocklistRepo
	eventBus                *scheduler.EventBus
	onDownloadCompleted     func(item *model.DownloadHistoryItem, storagePath string)
	onDownloadStatusChanged func(item *model.DownloadHistoryItem)
}

func NewSearchService(
	indexerRepo *repository.IndexerRepo,
	dlClientRepo *repository.DownloadClientRepo,
	dlHistoryRepo *repository.DownloadHistoryRepo,
	issueRepo *repository.IssueRepo,
	seriesRepo *repository.SeriesRepo,
	blocklistRepo *repository.BlocklistRepo,
	eventBus *scheduler.EventBus,
) *SearchService {
	return &SearchService{
		indexerRepo:   indexerRepo,
		dlClientRepo:  dlClientRepo,
		dlHistoryRepo: dlHistoryRepo,
		issueRepo:     issueRepo,
		seriesRepo:    seriesRepo,
		blocklistRepo: blocklistRepo,
		eventBus:      eventBus,
	}
}

// SetOnDownloadCompleted registers a callback invoked when a download completes.
func (s *SearchService) SetOnDownloadCompleted(fn func(item *model.DownloadHistoryItem, storagePath string)) {
	s.onDownloadCompleted = fn
}

func (s *SearchService) SetOnDownloadStatusChanged(fn func(item *model.DownloadHistoryItem)) {
	s.onDownloadStatusChanged = fn
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

	requireYear := shouldRequireSeriesYear(series, issue)
	strictQueries, looseQueries := buildSearchQueries(series, issue, requireYear)
	if requireYear && len(strictQueries) == 0 {
		requireYear = false
	}

	// Strict-first: when the series has a year and the issue is low-numbered,
	// run year-anchored queries first to avoid pulling in wrong-volume hits.
	// If they return nothing (indexer titles often don't include the year),
	// fall back to the loose queries instead of dead-ending. Scoring still
	// applies the year-mismatch penalty so a wrong-year result that does
	// surface from a loose query gets pushed below the score threshold.
	var primaryQueries, fallbackQueries []string
	if requireYear {
		primaryQueries = strictQueries
		fallbackQueries = looseQueries
	} else {
		primaryQueries = append(primaryQueries, strictQueries...)
		primaryQueries = append(primaryQueries, looseQueries...)
	}

	if len(primaryQueries) == 0 && len(fallbackQueries) == 0 {
		return nil, nil
	}

	allResults, seen := s.runQueries(ctx, primaryQueries, nil)

	if requireYear && len(allResults) == 0 && len(fallbackQueries) > 0 {
		year := 0
		if series.Year != nil {
			year = *series.Year
		}
		slog.Debug("strict ComicVine year search returned no results, falling back to loose queries",
			"series_id", series.ID,
			"issue_id", issue.ID,
			"year", year,
		)
		var more []newznab.SearchResult
		more, seen = s.runQueries(ctx, fallbackQueries, seen)
		allResults = append(allResults, more...)
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

// runQueries fans out the given query variants concurrently across all enabled
// indexers and returns the deduped union of results. The seen map carries
// across calls so a fallback pass doesn't re-emit hits the primary already saw.
func (s *SearchService) runQueries(ctx context.Context, queries []string, seen map[string]bool) ([]newznab.SearchResult, map[string]bool) {
	if len(queries) == 0 {
		if seen == nil {
			seen = make(map[string]bool)
		}
		return nil, seen
	}

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

	if seen == nil {
		seen = make(map[string]bool)
	}
	var merged []newznab.SearchResult
	for range queries {
		qr := <-ch
		for _, r := range qr.results {
			if !seen[r.GUID] {
				seen[r.GUID] = true
				merged = append(merged, r)
			}
		}
	}
	return merged, seen
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
func (s *SearchService) GrabResult(ctx context.Context, nzbURL, nzbName, nzbGuid string, size int64, indexerID int64, issueID *int64) (*model.DownloadHistoryItem, error) {
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
		NZBGuid:          nzbGuid,
		NZBURL:           nzbURL,
		ExternalID:       nzoID,
		Status:           model.DownloadStatusGrabbed,
		Size:             size,
	}
	if err := s.dlHistoryRepo.Create(item); err != nil {
		// Critical: callers (backlog queue, auto-search) use the returned
		// item.ID as a foreign key — silently continuing here would attach
		// downloads with FK=0 against a row that doesn't exist. Surface
		// the failure so the caller can mark the backlog item failed and
		// the user sees a real error in last_error.
		return nil, fmt.Errorf("recording download history: %w", err)
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

// GrabOutcome carries the result of an auto-search/grab attempt. When Item
// is non-nil the grab succeeded. When Item is nil, Reason explains why no
// grab happened — populated for the queue worker to surface in last_error
// instead of the previous opaque "no nzb found".
type GrabOutcome struct {
	Item   *model.DownloadHistoryItem
	Reason string
}

// AutoSearchAndGrab searches for an issue and grabs the best result.
// Returns a non-nil GrabOutcome describing the disposition. err is reserved
// for unexpected failures (DB errors, search subsystem errors); user-facing
// "nothing to do" outcomes (already grabbed, no hits, all below threshold)
// land in GrabOutcome.Reason with Item=nil.
func (s *SearchService) AutoSearchAndGrab(ctx context.Context, issueID int64) (*GrabOutcome, error) {
	// Check for duplicate grabs first
	exists, err := s.dlHistoryRepo.ExistsForIssue(issueID)
	if err != nil {
		return nil, fmt.Errorf("checking for existing download: %w", err)
	}
	if exists {
		return &GrabOutcome{Reason: "already grabbed"}, nil
	}

	results, err := s.SearchForIssue(ctx, issueID)
	if err != nil {
		return nil, fmt.Errorf("searching for issue %d: %w", issueID, err)
	}

	if len(results) == 0 {
		return &GrabOutcome{Reason: "no indexer hits — try a broader match or check Prowlarr categories"}, nil
	}

	// Filter to results meeting the minimum score threshold
	const minScore = 50
	var qualified []ScoredResult
	for _, r := range results {
		if r.Score >= minScore {
			qualified = append(qualified, r)
		}
	}
	if len(qualified) == 0 {
		// Build a descriptive reason: top 3 titles + scores so the user
		// can see what the indexer returned and why it scored too low.
		top := results
		if len(top) > 3 {
			top = top[:3]
		}
		parts := make([]string, 0, len(top))
		for _, r := range top {
			parts = append(parts, fmt.Sprintf("%q=%d", r.Title, r.Score))
		}
		reason := fmt.Sprintf("no hit above score %d; top %d of %d: %s",
			minScore, len(top), len(results), strings.Join(parts, "; "))
		slog.Debug("no results meet score threshold",
			"issue_id", issueID, "best_score", results[0].Score, "min_score", minScore)
		return &GrabOutcome{Reason: reason}, nil
	}

	// Sort qualified results by grabs descending, ties broken by score
	sort.Slice(qualified, func(i, j int) bool {
		if qualified[i].Grabs != qualified[j].Grabs {
			return qualified[i].Grabs > qualified[j].Grabs
		}
		return qualified[i].Score > qualified[j].Score
	})

	best := qualified[0]
	slog.Info("auto-grab selected",
		"issue_id", issueID,
		"title", best.Title,
		"grabs", best.Grabs,
		"score", best.Score,
	)

	item, err := s.GrabResult(ctx, best.NZBURL, best.Title, best.GUID, best.Size, best.IndexerID, &issueID)
	if err != nil {
		return nil, fmt.Errorf("grabbing %q: %w", best.Title, err)
	}
	return &GrabOutcome{Item: item}, nil
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

		slot, err := sabClient.GetSlotStatus(item.ExternalID)
		if err != nil {
			slog.Warn("error checking download status", "nzo_id", item.ExternalID, "error", err)
			continue
		}
		if !slot.Found {
			continue
		}

		var newStatus model.DownloadStatus
		switch strings.ToLower(slot.Status) {
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
			if err := s.dlHistoryRepo.UpdateStatus(item.ID, newStatus, slot.Status); err != nil {
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
			item.Status = newStatus
			item.Message = slot.Status
			if s.onDownloadStatusChanged != nil {
				updated := item
				s.onDownloadStatusChanged(&updated)
			}
			slog.Info("download status updated",
				"id", item.ID,
				"nzb", item.NZBName,
				"status", newStatus,
			)

			// Auto-blocklist failed downloads
			if newStatus == model.DownloadStatusFailed && s.blocklistRepo != nil && item.NZBGuid != "" {
				if err := s.blocklistRepo.Add(item.NZBGuid, item.NZBName, "download failed"); err != nil {
					slog.Warn("failed to add to blocklist", "guid", item.NZBGuid, "error", err)
				} else {
					slog.Info("added failed download to blocklist", "guid", item.NZBGuid, "nzb", item.NZBName)
				}
			}

			// Trigger post-processing for completed downloads
			if newStatus == model.DownloadStatusCompleted && slot.Storage != "" && s.onDownloadCompleted != nil {
				go s.onDownloadCompleted(&item, slot.Storage)
			}
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

			results, err := client.SearchCtx(ctx, query, categories)
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

	// Deduplicate by GUID, filter blocklist, and reject non-comic releases.
	// Without the format filter, indexers happily return EXE installers,
	// PDFs, ebooks, and video rips that share words with the search query
	// (e.g. "Wolverine" finds Wolverine.2013.MULTi.1080p.BluRay.mkv).
	// LongBox only knows how to read CBR/CBZ/CB7 archives — anything else
	// is unusable, so we drop it before scoring.
	seen := make(map[string]bool)
	deduped := make([]newznab.SearchResult, 0, len(allResults))
	for _, r := range allResults {
		if seen[r.GUID] {
			continue
		}
		seen[r.GUID] = true
		if !isComicRelease(r.Title) {
			slog.Debug("dropping non-comic release",
				"title", r.Title, "indexer", r.IndexerName)
			continue
		}
		if s.blocklistRepo != nil && r.GUID != "" {
			if blocked, err := s.blocklistRepo.IsBlocked(r.GUID); err == nil && blocked {
				continue
			}
		}
		deduped = append(deduped, r)
	}

	return deduped, nil
}

// disallowedReleaseExtensions lists file extensions that unmistakably mark a
// release as something other than a comic. Matched as a whole word in the
// release title (case-insensitive) — substrings inside other words don't
// trigger (e.g. "AVI" inside a title isn't filtered, but ".avi" is).
//
// .zip / .rar / .7z are intentionally NOT here: they're sometimes used for
// scanned-comic archives. CBZ files are zip archives, sometimes mislabeled.
var disallowedReleaseExtensions = []string{
	// Executables / installers
	".exe", ".msi", ".dmg", ".pkg", ".deb", ".rpm", ".apk", ".bat", ".sh",
	// Documents / ebooks (NOT comic formats)
	".pdf", ".epub", ".mobi", ".azw", ".azw3", ".kf8", ".prc", ".djvu", ".doc", ".docx",
	// Video
	".mkv", ".mp4", ".avi", ".mov", ".wmv", ".flv", ".m4v", ".webm", ".ts", ".m2ts",
	// Audio
	".mp3", ".flac", ".wav", ".ogg", ".m4a", ".aac", ".wma", ".opus",
	// Disc images
	".iso", ".bin", ".cue", ".nrg",
	// Source / build artifacts
	".tar", ".gz", ".bz2", ".xz",
}

// isComicRelease reports whether the given release title is a plausible
// comic-format archive. The check is conservative — it accepts anything
// that doesn't contain a known non-comic extension AND prefers titles
// that explicitly contain `.cbr` / `.cbz` / `.cb7`. Most scene-style
// comic releases omit the extension entirely (e.g. "Alice.Never.After.001.
// (2022).(Empire)"), so we don't require .cbz/.cbr presence — only
// rejecting on positive evidence of a non-comic format.
func isComicRelease(title string) bool {
	t := strings.ToLower(title)

	// Hard-allow when the title explicitly carries a comic extension.
	if strings.Contains(t, ".cbz") || strings.Contains(t, ".cbr") || strings.Contains(t, ".cb7") {
		return true
	}

	for _, ext := range disallowedReleaseExtensions {
		// Match the extension only when followed by a word boundary so
		// random substrings (e.g. ".isolated") don't trigger.
		if idx := strings.Index(t, ext); idx >= 0 {
			end := idx + len(ext)
			if end == len(t) {
				return false
			}
			c := t[end]
			if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') {
				return false
			}
		}
	}
	return true
}

// buildSearchQueries returns multiple query strings to try against indexers.
//
// Naming conventions on Usenet:
//   - Scene/p2p releases use "Title 001" or "Title.001.(Year)" — never "#".
//     The "#" symbol breaks matches because indexer search engines treat the
//     query as a literal substring and no release title contains "#".
//   - Issue numbers are usually zero-padded to three digits ("001"), even
//     for double-digit issues ("027").
//
// We generate the raw forms that actually appear in release titles. The
// "#" variants are kept as last-resort fallbacks for indexers that index
// release subjects from forums where "#" does occasionally appear.
func buildSearchQueries(series *model.Series, issue *model.Issue, requireYear bool) ([]string, []string) {
	seenStrict := make(map[string]bool)
	seenLoose := make(map[string]bool)
	add := func(target *[]string, seen map[string]bool, q string) {
		q = strings.TrimSpace(q)
		if q == "" || seen[q] {
			return
		}
		seen[q] = true
		*target = append(*target, q)
	}

	title := strings.TrimSpace(series.Title)
	if title == "" {
		title = "untitled series"
	}
	num := strings.TrimSpace(issue.IssueNumber)

	var strict []string
	var loose []string

	addLoose := func(q string) {
		add(&loose, seenLoose, q)
	}
	addStrict := func(q string) {
		add(&strict, seenStrict, q)
	}

	// Issue number variants we'll try.
	var nums []string
	if num != "" {
		// Strip a leading zero from "01" → "1" so we don't dedupe-skip it
		// when input is already padded.
		bare := strings.TrimLeft(num, "0")
		if bare == "" {
			bare = "0"
		}
		nums = append(nums, bare)
		// Three-digit padded form (Empire/Pyrate/etc. always use this).
		if len(bare) < 3 {
			nums = append(nums, fmt.Sprintf("%03s", bare))
		}
		// Two-digit padded for Marvel/DC continuity series.
		if len(bare) < 2 {
			nums = append(nums, fmt.Sprintf("%02s", bare))
		}
	}

	// Loose: raw release-style queries — "Title 001", "Title 1".
	if len(nums) > 0 {
		for _, n := range nums {
			addLoose(fmt.Sprintf("%s %s", title, n))
		}
		// "#" variants AFTER the bare ones so they only fire as fallbacks.
		for _, n := range nums {
			addLoose(fmt.Sprintf("%s #%s", title, n))
		}
	} else {
		addLoose(title)
	}

	// Year-anchored queries — for low-numbered issues of series that share
	// titles across multiple volumes (e.g., "Detective Comics #1").
	if series.Year != nil {
		year := *series.Year
		var yearQueries []string
		if len(nums) > 0 {
			for _, n := range nums {
				yearQueries = append(yearQueries,
					fmt.Sprintf("%s %s %d", title, n, year),
					fmt.Sprintf("%s %d %s", title, year, n),
					fmt.Sprintf("%s (%d) %s", title, year, n),
				)
			}
		} else {
			yearQueries = append(yearQueries,
				fmt.Sprintf("%s %d", title, year),
				fmt.Sprintf("%s (%d)", title, year),
			)
		}

		for _, q := range yearQueries {
			if requireYear {
				addStrict(q)
			} else {
				addLoose(q)
			}
		}
	}

	return strict, loose
}

func shouldRequireSeriesYear(series *model.Series, issue *model.Issue) bool {
	if series == nil || issue == nil || series.Year == nil {
		return false
	}
	return isLowIssueNumber(issue)
}

// yearPattern matches 4-digit years (1900–2099) in NZB titles.
var yearPattern = regexp.MustCompile(`(?:^|[\s._(-])(\d{4})(?:[\s._)\-]|$)`)

// issueNumberCutPattern finds the first issue-number-like token in a result
// title, used to slice off the trailing "001 (2024) (digital)" tail so the
// leading portion can be compared as a claimed series name.
var issueNumberCutPattern = regexp.MustCompile(`(?i)[\s._-]+#?\d{1,4}(?:[\s._(\-]|$)`)

// indexerTitleStopwords are dropped before comparing token sets — they convey
// no series identity and would otherwise inflate the "extra word" count.
var indexerTitleStopwords = map[string]bool{
	"the": true, "of": true, "and": true, "vs": true, "with": true,
	"from": true, "for": true, "a": true, "an": true, "to": true,
	"in": true, "on": true, "at": true, "by": true, "into": true, "onto": true,
}

// significantTokens lowercases, splits on non-alphanumerics, and returns the
// set of tokens longer than 2 chars that aren't stopwords. Used on both the
// LB series title and the claimed series portion of an indexer result.
func significantTokens(s string) map[string]bool {
	tokens := make(map[string]bool)
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if len(w) > 2 && !indexerTitleStopwords[w] {
			tokens[w] = true
		}
	}
	return tokens
}

// claimedSeriesTokens extracts the series-name token set from an indexer-style
// result title by cutting at the first issue-number or year marker. This
// prevents "Lady Rawhide - Lady Zorro 001 (2015)" from being treated as a
// "Zorro" match just because the word zorro appears somewhere in the title.
func claimedSeriesTokens(resultTitle string) map[string]bool {
	cut := strings.ToLower(resultTitle)
	if loc := issueNumberCutPattern.FindStringIndex(cut); loc != nil {
		cut = cut[:loc[0]]
	} else if loc := yearPattern.FindStringIndex(cut); loc != nil {
		cut = cut[:loc[0]]
	}
	return significantTokens(cut)
}

func scoreResult(result newznab.SearchResult, series *model.Series, issue *model.Issue) int {
	score := 0
	titleLower := strings.ToLower(result.Title)

	// Title matching by token set. The old check (substring contains) accepted
	// "Lady Rawhide - Lady Zorro" as a "Zorro" match because the substring
	// "zorro" appears in it. Now: pull the claimed series portion (everything
	// before the issue number) and compare token sets. Extra distinguishing
	// words like "Lady" or "Rawhide" become a strong negative signal.
	seriesTokens := significantTokens(series.Title)
	claimedTokens := claimedSeriesTokens(result.Title)
	if len(seriesTokens) > 0 {
		matched := 0
		extra := 0
		for t := range claimedTokens {
			if seriesTokens[t] {
				matched++
			} else {
				extra++
			}
		}
		switch {
		case matched == len(seriesTokens) && extra == 0:
			score += 60 // clean token-set match
		case matched == len(seriesTokens) && extra == 1:
			score += 30 // one extra word — possibly a subtitle, allow
		case matched == len(seriesTokens):
			score -= 100 // claimed name has 2+ distinguishing words — wrong volume
		default:
			// Missing series tokens in result. Partial credit proportional to coverage.
			score += (matched * 30) / len(seriesTokens)
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
		year := *series.Year
		yearStr := fmt.Sprintf("%d", year)
		hasCorrectYear := strings.Contains(titleLower, yearStr)
		maxWrongDiff := 0
		matches := yearPattern.FindAllStringSubmatch(result.Title, -1)
		for _, m := range matches {
			if y, err := strconv.Atoi(m[1]); err == nil && y >= 1900 && y <= 2099 {
				if y == year {
					hasCorrectYear = true
					continue
				}
				diff := y - year
				if diff < 0 {
					diff = -diff
				}
				if diff > maxWrongDiff {
					maxWrongDiff = diff
				}
			}
		}

		if hasCorrectYear {
			score += 25
		} else if maxWrongDiff > 0 {
			if isLowIssueNumber(issue) {
				switch {
				case maxWrongDiff >= 5:
					score -= 80
				case maxWrongDiff >= 3:
					score -= 60
				default:
					score -= 40
				}
			} else if maxWrongDiff > 3 {
				score -= 20
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

func isLowIssueNumber(issue *model.Issue) bool {
	if issue == nil {
		return false
	}
	if issue.SortNumber > 0 && issue.SortNumber <= 25 {
		return true
	}
	if n, err := strconv.ParseFloat(strings.TrimSpace(issue.IssueNumber), 64); err == nil {
		return n > 0 && n <= 25
	}
	return false
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

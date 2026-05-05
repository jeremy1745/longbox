package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
)

// BacklogQueue is the worker pool that drains backlog_items by searching
// indexers and dispatching NZBs to the download client. Workers run
// concurrently and pull from the same atomic claim — `repo.ClaimNextCandidate`
// transitions a row from pending/failed → searching in one statement, so two
// workers never grab the same item.
//
// Sizing: maxConcurrent caps how many items can be "in flight" against
// indexers + the download client. Default 25 mirrors typical SAB/NZBGet
// concurrency and the per-indexer rate-limiter handles back-pressure.
type BacklogQueue struct {
	repo          *repository.BacklogRepo
	searchSvc     *SearchService
	backlogSvc    *BacklogService
	settings      BacklogSettings
	maxConcurrent int

	stopCh chan struct{}
	wakeCh chan struct{} // pulse when new work arrives so workers don't wait the full tick
	wg     sync.WaitGroup
}

func NewBacklogQueue(repo *repository.BacklogRepo, searchSvc *SearchService, backlogSvc *BacklogService, settings BacklogSettings, maxConcurrent int) *BacklogQueue {
	if maxConcurrent <= 0 {
		maxConcurrent = 25
	}
	return &BacklogQueue{
		repo:          repo,
		searchSvc:     searchSvc,
		backlogSvc:    backlogSvc,
		settings:      settings,
		maxConcurrent: maxConcurrent,
		stopCh:        make(chan struct{}),
		wakeCh:        make(chan struct{}, 1),
	}
}

// Start spawns the worker pool. Each worker independently claims and
// processes the next eligible item. Workers idle on a 5s timer when the
// queue is empty; Wake() can be called externally to drain a freshly-
// inserted run without waiting for the next tick.
func (q *BacklogQueue) Start() {
	for i := 0; i < q.maxConcurrent; i++ {
		q.wg.Add(1)
		go q.worker(i)
	}
}

// Wake nudges idle workers to re-check immediately. Cheap, non-blocking —
// extra wakes coalesce into the single-buffer wakeCh.
func (q *BacklogQueue) Wake() {
	select {
	case q.wakeCh <- struct{}{}:
	default:
	}
}

func (q *BacklogQueue) Stop() {
	close(q.stopCh)
	q.wg.Wait()
}

func (q *BacklogQueue) worker(id int) {
	defer q.wg.Done()
	idleTicker := time.NewTicker(5 * time.Second)
	defer idleTicker.Stop()
	for {
		// Process as long as there's work; only sleep when claim returns nil.
		didWork := q.processOne()
		if didWork {
			// Loop back immediately — there might be more queued.
			select {
			case <-q.stopCh:
				return
			default:
				continue
			}
		}
		select {
		case <-q.stopCh:
			return
		case <-idleTicker.C:
		case <-q.wakeCh:
			// Wake one worker; others will pick up via idleTicker if more work
			// was inserted. Re-pulse so a sibling worker can also wake on the
			// next loop iteration if there are multiple new items.
			q.Wake()
		}
	}
}

// processOne atomically claims the next eligible backlog item and walks it
// through the search → grab → attach pipeline. Returns true when an item
// was processed (regardless of outcome) so the caller can immediately try
// again instead of waiting on the idle ticker.
func (q *BacklogQueue) processOne() bool {
	candidate, err := q.repo.ClaimNextCandidate(q.settings.MaxRetries, time.Now().UTC())
	if err != nil {
		slog.Warn("backlog queue: claim", "error", err)
		return false
	}
	if candidate == nil {
		return false
	}

	// Pre-check: if the issue is already owned (file on disk) or already
	// completed (terminal download_history row), there's nothing to search
	// for. Mark it completed so it stops cluttering the failed pile.
	if owned, err := q.repo.IssueHasFileOrGrab(candidate.IssueID); err != nil {
		slog.Warn("backlog queue: owned-precheck", "item_id", candidate.ID, "error", err)
	} else if owned {
		if err := q.repo.MarkCompleted(candidate.ID); err != nil {
			slog.Warn("backlog queue: mark completed", "item_id", candidate.ID, "error", err)
			return true
		}
		q.backlogSvc.PublishItemUpdate(candidate.ID)
		if err := q.repo.RefreshRunCounts(candidate.BacklogRunID); err != nil {
			slog.Warn("backlog queue: refresh run", "run_id", candidate.BacklogRunID, "error", err)
		}
		q.backlogSvc.PublishRunUpdate(candidate.BacklogRunID)
		return true
	}

	// ClaimNextCandidate already set status=searching atomically; just
	// publish the update + activity event for live UI.
	q.backlogSvc.PublishItemUpdate(candidate.ID)
	q.publishActivity(candidate, "searching", "Searching indexers")

	ctx, cancel := q.searchContext()
	defer cancel()

	outcome, err := q.searchSvc.AutoSearchAndGrab(ctx, candidate.IssueID)
	if err != nil {
		slog.Warn("backlog queue: auto-search failed", "item_id", candidate.ID, "error", err)
		q.failItem(candidate, err.Error())
		q.publishActivity(candidate, "idle", "Search failed")
		return true
	}
	if outcome == nil || outcome.Item == nil {
		reason := "no nzb found"
		if outcome != nil && outcome.Reason != "" {
			reason = outcome.Reason
		}
		slog.Info("backlog queue: not grabbed", "item_id", candidate.ID, "reason", reason)
		// "already grabbed" → relink to existing download_history row so
		// HandleDownloadStatus can drive the terminal state when SAB updates.
		// See RelinkToExistingDownload for the rationale.
		if outcome != nil && outcome.Reason == "already grabbed" {
			if relinked, err := q.repo.RelinkToExistingDownload(candidate.ID, candidate.IssueID); err == nil && relinked {
				q.backlogSvc.PublishItemUpdate(candidate.ID)
				_ = q.repo.RefreshRunCounts(candidate.BacklogRunID)
				q.backlogSvc.PublishRunUpdate(candidate.BacklogRunID)
				q.publishActivity(candidate, "idle", "Linked to existing download")
				return true
			}
		}
		q.failItem(candidate, reason)
		q.publishActivity(candidate, "idle", reason)
		return true
	}

	q.publishActivity(candidate, "grabbing", "Sending NZB to download client")
	result := outcome.Item
	sabID := result.ExternalID
	if err := q.repo.AttachDownload(candidate.ID, result.ID, sabID, result.NZBGuid); err != nil {
		slog.Warn("backlog queue: attach download", "item_id", candidate.ID, "error", err)
		q.failItem(candidate, "attach_download: "+err.Error())
		q.publishActivity(candidate, "idle", "Attach failed")
		return true
	}
	q.backlogSvc.PublishItemUpdate(candidate.ID)
	q.publishActivity(candidate, "idle", "Queued for download")

	if err := q.repo.RefreshRunCounts(candidate.BacklogRunID); err != nil {
		slog.Warn("backlog queue: refresh run", "run_id", candidate.BacklogRunID, "error", err)
	}
	q.backlogSvc.PublishRunUpdate(candidate.BacklogRunID)
	return true
}

// searchContext bounds a single search+grab to 2 minutes AND aborts on Stop().
func (q *BacklogQueue) searchContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	go func() {
		select {
		case <-q.stopCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (q *BacklogQueue) publishActivity(item *model.BacklogItem, stage, message string) {
	if item == nil || q.backlogSvc == nil {
		return
	}
	q.backlogSvc.PublishActivity(BacklogActivity{
		Stage:       stage,
		ItemID:      item.ID,
		RunID:       item.BacklogRunID,
		IssueID:     item.IssueID,
		IssueNumber: item.IssueNumber,
		SeriesTitle: item.SeriesTitle,
		Message:     message,
		StartedAt:   time.Now().UTC().Format(time.RFC3339),
	})
}

func (q *BacklogQueue) failItem(item *model.BacklogItem, message string) {
	attempt := item.RetryCount + 1
	exhausted := q.settings.MaxRetries > 0 && attempt >= q.settings.MaxRetries
	retryAt := q.nextRetryTime(attempt)
	if exhausted {
		retryAt = nil
	}
	if err := q.repo.MarkFailure(item.ID, message, retryAt, exhausted); err != nil {
		slog.Warn("backlog queue: mark failure", "item_id", item.ID, "error", err)
	}
	q.backlogSvc.PublishItemUpdate(item.ID)
	if err := q.repo.RefreshRunCounts(item.BacklogRunID); err != nil {
		slog.Warn("backlog queue: refresh run", "run_id", item.BacklogRunID, "error", err)
	}
	q.backlogSvc.PublishRunUpdate(item.BacklogRunID)
}

func (q *BacklogQueue) nextRetryTime(attempt int) *time.Time {
	if attempt <= 0 {
		return nil
	}
	if len(q.settings.RetryBackoffMinutes) == 0 {
		return nil
	}
	idx := attempt - 1
	if idx >= len(q.settings.RetryBackoffMinutes) {
		idx = len(q.settings.RetryBackoffMinutes) - 1
	}
	dur := time.Duration(q.settings.RetryBackoffMinutes[idx]) * time.Minute
	t := time.Now().UTC().Add(dur)
	return &t
}

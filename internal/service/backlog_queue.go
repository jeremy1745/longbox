package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
)

type BacklogQueue struct {
	repo          *repository.BacklogRepo
	searchSvc     *SearchService
	backlogSvc    *BacklogService
	settings      BacklogSettings
	maxConcurrent int
	stopCh        chan struct{}
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
	}
}

func (q *BacklogQueue) Start() {
	go q.loop()
}

func (q *BacklogQueue) Stop() {
	close(q.stopCh)
}

func (q *BacklogQueue) loop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		q.tick()
		select {
		case <-q.stopCh:
			return
		case <-ticker.C:
		}
	}
}

func (q *BacklogQueue) tick() {
	active, err := q.repo.CountActiveDownloads()
	if err != nil {
		slog.Warn("backlog queue: count active", "error", err)
		return
	}
	if active >= q.maxConcurrent {
		return
	}

	candidate, err := q.repo.FindNextCandidate(q.settings.MaxRetries, time.Now().UTC())
	if err != nil {
		slog.Warn("backlog queue: find candidate", "error", err)
		return
	}
	if candidate == nil {
		return
	}

	if err := q.repo.UpdateItemStatus(candidate.ID, "searching", "", nil); err != nil {
		slog.Warn("backlog queue: mark searching", "item_id", candidate.ID, "error", err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	result, err := q.searchSvc.AutoSearchAndGrab(ctx, candidate.IssueID)
	if err != nil {
		slog.Warn("backlog queue: auto-search failed", "item_id", candidate.ID, "error", err)
		q.failItem(candidate, err.Error())
		return
	}
	if result == nil {
		slog.Info("backlog queue: no nzb found", "item_id", candidate.ID)
		q.failItem(candidate, "no nzb found")
		return
	}

	sabID := result.ExternalID
	if err := q.repo.AttachDownload(candidate.ID, result.ID, sabID, result.NZBGuid); err != nil {
		slog.Warn("backlog queue: attach download", "item_id", candidate.ID, "error", err)
		return
	}

	if err := q.repo.RefreshRunCounts(candidate.BacklogRunID); err != nil {
		slog.Warn("backlog queue: refresh run", "run_id", candidate.BacklogRunID, "error", err)
	}
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
	if err := q.repo.RefreshRunCounts(item.BacklogRunID); err != nil {
		slog.Warn("backlog queue: refresh run", "run_id", item.BacklogRunID, "error", err)
	}
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

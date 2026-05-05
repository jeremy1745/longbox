package service

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scheduler"
)

var (
	ErrSeriesNotFound      = errors.New("series not found")
	ErrBacklogItemNotFound = errors.New("backlog item not found")
)

type BacklogSettings struct {
	DefaultIncludeVariants bool
	MaxRetries             int
	RetryBackoffMinutes    []int
}

type BacklogService struct {
	backlogRepo *repository.BacklogRepo
	issueRepo   *repository.IssueRepo
	seriesRepo  *repository.SeriesRepo
	eventBus    *scheduler.EventBus

	defaultIncludeVariants bool
	maxRetries             int
	retryBackoff           []int

	// onQueueDirty (set via SetOnQueueDirty) is called when new items
	// become eligible for the queue worker — CreateRun and RetryAllInRun
	// pulse it so the pool wakes up immediately instead of waiting on its
	// 5-second idle tick. nil-safe.
	onQueueDirty func()
}

// SetOnQueueDirty installs a callback that fires whenever this service
// inserts or wakes up backlog items that the queue worker should pick up.
// The wiring lives in main.go so the service doesn't import the queue.
func (s *BacklogService) SetOnQueueDirty(fn func()) {
	s.onQueueDirty = fn
}

func (s *BacklogService) wakeQueue() {
	if s.onQueueDirty != nil {
		s.onQueueDirty()
	}
}

func NewBacklogService(backlogRepo *repository.BacklogRepo, issueRepo *repository.IssueRepo, seriesRepo *repository.SeriesRepo, eventBus *scheduler.EventBus, settings BacklogSettings) *BacklogService {
	return &BacklogService{
		backlogRepo:            backlogRepo,
		issueRepo:              issueRepo,
		seriesRepo:             seriesRepo,
		eventBus:               eventBus,
		defaultIncludeVariants: settings.DefaultIncludeVariants,
		maxRetries:             settings.MaxRetries,
		retryBackoff:           settings.RetryBackoffMinutes,
	}
}

func (s *BacklogService) CreateRun(seriesID int64, includeVariants *bool) (*model.BacklogRun, error) {
	series, err := s.seriesRepo.GetByID(seriesID)
	if err != nil {
		return nil, fmt.Errorf("lookup series: %w", err)
	}
	if series == nil {
		return nil, ErrSeriesNotFound
	}

	flag := s.defaultIncludeVariants
	if includeVariants != nil {
		flag = *includeVariants
	}

	run, err := s.backlogRepo.CreateRun(seriesID, flag)
	if err != nil {
		return nil, err
	}

	missingIssues, err := s.collectMissingIssues(seriesID)
	if err != nil {
		return nil, err
	}

	items := make([]model.BacklogItem, 0, len(missingIssues))
	for _, issue := range missingIssues {
		items = append(items, model.BacklogItem{
			BacklogRunID: run.ID,
			SeriesID:     seriesID,
			IssueID:      issue.ID,
			Priority:     0,
			Status:       "pending",
		})
	}

	if err := s.backlogRepo.InsertItems(items); err != nil {
		return nil, err
	}

	total := len(items)
	queued := total
	completed := 0
	failed := 0
	status := "ready"
	if total == 0 {
		status = "completed"
	}
	if err := s.backlogRepo.UpdateRunCounts(run.ID, total, queued, completed, failed, status); err != nil {
		return nil, err
	}

	updated, err := s.backlogRepo.GetRunByID(run.ID)
	if err != nil {
		return nil, err
	}

	s.publishRunUpdate(updated)
	s.wakeQueue()
	return updated, nil
}

func (s *BacklogService) HandleDownloadStatus(item *model.DownloadHistoryItem) {
	if item == nil {
		return
	}
	backlogItem, err := s.backlogRepo.FindByDownloadHistory(item.ID)
	if err != nil || backlogItem == nil {
		return
	}

	switch item.Status {
	case model.DownloadStatusDownloading:
		_ = s.backlogRepo.UpdateItemStatus(backlogItem.ID, "downloading", "", nil)
	case model.DownloadStatusCompleted:
		_ = s.backlogRepo.MarkCompleted(backlogItem.ID)
	case model.DownloadStatusFailed, model.DownloadStatusImportFailed:
		nextRetry := s.nextRetryTime(backlogItem.RetryCount + 1)
		exhausted := s.maxRetries > 0 && backlogItem.RetryCount+1 >= s.maxRetries
		_ = s.backlogRepo.MarkFailure(backlogItem.ID, item.Message, nextRetry, exhausted)
	}

	if err := s.backlogRepo.RefreshRunCounts(backlogItem.BacklogRunID); err != nil {
		slog.Warn("failed to refresh run counts", "run_id", backlogItem.BacklogRunID, "error", err)
	}
	s.PublishRunUpdate(backlogItem.BacklogRunID)
}

func (s *BacklogService) collectMissingIssues(seriesID int64) ([]model.Issue, error) {
	issues, err := s.issueRepo.ListBySeries(seriesID)
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	return filterMissingIssues(issues), nil
}

// filterMissingIssues returns issues that should be queued: no local file and
// no skip status set. Pure function exposed for unit testing.
func filterMissingIssues(issues []model.Issue) []model.Issue {
	missing := make([]model.Issue, 0, len(issues))
	for _, issue := range issues {
		if issue.HasFile {
			continue
		}
		if issue.SkipStatus != nil {
			continue
		}
		missing = append(missing, issue)
	}
	return missing
}

func (s *BacklogService) nextRetryTime(attempt int) *time.Time {
	if attempt <= 0 || len(s.retryBackoff) == 0 {
		return nil
	}
	idx := attempt - 1
	if idx >= len(s.retryBackoff) {
		idx = len(s.retryBackoff) - 1
	}
	dur := time.Duration(s.retryBackoff[idx]) * time.Minute
	t := time.Now().UTC().Add(dur)
	return &t
}

func (s *BacklogService) PauseRun(id int64) (*model.BacklogRun, error) {
	if err := s.backlogRepo.SetRunPaused(id, true); err != nil {
		return nil, err
	}
	run, err := s.backlogRepo.GetRunByID(id)
	if err != nil {
		return nil, err
	}
	s.publishRunUpdate(run)
	return run, nil
}

func (s *BacklogService) ResumeRun(id int64) (*model.BacklogRun, error) {
	if err := s.backlogRepo.SetRunPaused(id, false); err != nil {
		return nil, err
	}
	run, err := s.backlogRepo.GetRunByID(id)
	if err != nil {
		return nil, err
	}
	s.publishRunUpdate(run)
	return run, nil
}

// RetryAllInRun resets every failed/errored item in the run back to pending so
// the queue worker picks them up on its next tick. Returns the count and the
// refreshed run.
func (s *BacklogService) RetryAllInRun(runID int64) (int64, *model.BacklogRun, error) {
	run, err := s.backlogRepo.GetRunByID(runID)
	if err != nil {
		return 0, nil, err
	}
	if run == nil {
		return 0, nil, sql.ErrNoRows
	}
	count, err := s.backlogRepo.RetryAllInRun(runID)
	if err != nil {
		return 0, nil, err
	}
	if err := s.backlogRepo.RefreshRunCounts(runID); err != nil {
		slog.Warn("failed to refresh run counts after global retry", "run_id", runID, "error", err)
	}
	updated, _ := s.backlogRepo.GetRunByID(runID)
	if updated != nil {
		s.publishRunUpdate(updated)
	}
	s.wakeQueue()
	return count, updated, nil
}

func (s *BacklogService) RetryItem(id int64) (*model.BacklogItem, error) {
	item, err := s.backlogRepo.RetryItem(id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrBacklogItemNotFound
		}
		return nil, err
	}
	defer s.wakeQueue()
	if err := s.backlogRepo.RefreshRunCounts(item.BacklogRunID); err != nil {
		slog.Warn("failed to refresh run counts after retry", "run_id", item.BacklogRunID, "error", err)
	}
	s.PublishRunUpdate(item.BacklogRunID)
	s.PublishItemUpdate(item.ID)
	return item, nil
}

func (s *BacklogService) PublishRunUpdate(runID int64) {
	run, err := s.backlogRepo.GetRunByID(runID)
	if err != nil {
		return
	}
	s.publishRunUpdate(run)
}

func (s *BacklogService) publishRunUpdate(run *model.BacklogRun) {
	if s.eventBus == nil || run == nil {
		return
	}
	s.eventBus.Publish(scheduler.Event{Type: "backlog:run", Data: run})
}

// PublishItemUpdate emits a backlog:item SSE event for the current state of
// the given backlog item. Safe to call after any state transition; silently
// no-ops if the item can no longer be fetched (e.g. retried-and-deleted).
func (s *BacklogService) PublishItemUpdate(itemID int64) {
	if s.eventBus == nil {
		return
	}
	item, err := s.backlogRepo.FindByID(itemID)
	if err != nil || item == nil {
		return
	}
	s.eventBus.Publish(scheduler.Event{Type: "backlog:item", Data: item})
}

// BacklogActivity describes what the queue worker is doing right now.
type BacklogActivity struct {
	Stage       string `json:"stage"` // "searching", "grabbing", "idle"
	ItemID      int64  `json:"item_id,omitempty"`
	RunID       int64  `json:"run_id,omitempty"`
	IssueID     int64  `json:"issue_id,omitempty"`
	IssueNumber string `json:"issue_number,omitempty"`
	SeriesTitle string `json:"series_title,omitempty"`
	Message     string `json:"message,omitempty"`
	StartedAt   string `json:"started_at"`
}

// PublishActivity emits a backlog:activity SSE event so the UI can show
// "now searching ..." in real time.
func (s *BacklogService) PublishActivity(a BacklogActivity) {
	if s.eventBus == nil {
		return
	}
	s.eventBus.Publish(scheduler.Event{Type: "backlog:activity", Data: a})
}

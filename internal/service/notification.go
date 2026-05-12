package service

import (
	"fmt"
	"log/slog"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scheduler"
	"github.com/jeremy/longbox/internal/slack"
)

// NotificationService listens to EventBus events and sends Slack notifications.
type NotificationService struct {
	settingRepo   *repository.SettingRepo
	eventBus      *scheduler.EventBus
	dlHistoryRepo *repository.DownloadHistoryRepo
	eventCh       chan scheduler.Event
	stopCh        chan struct{}
}

// NewNotificationService creates a new notification service.
func NewNotificationService(
	settingRepo *repository.SettingRepo,
	eventBus *scheduler.EventBus,
	dlHistoryRepo *repository.DownloadHistoryRepo,
) *NotificationService {
	return &NotificationService{
		settingRepo:   settingRepo,
		eventBus:      eventBus,
		dlHistoryRepo: dlHistoryRepo,
		stopCh:        make(chan struct{}),
	}
}

// Start subscribes to EventBus and launches the listener goroutine.
func (s *NotificationService) Start() {
	s.eventCh = s.eventBus.Subscribe()
	go s.listen()
	slog.Info("notification service started")
}

// Stop shuts down the listener goroutine and unsubscribes from the EventBus.
func (s *NotificationService) Stop() {
	close(s.stopCh)
	s.eventBus.Unsubscribe(s.eventCh)
	slog.Info("notification service stopped")
}

func (s *NotificationService) listen() {
	for {
		select {
		case <-s.stopCh:
			return
		case evt, ok := <-s.eventCh:
			if !ok {
				return
			}
			s.handleEvent(evt)
		}
	}
}

func (s *NotificationService) handleEvent(evt scheduler.Event) {
	// Check global slack enabled
	enabled, err := s.settingRepo.Get("slack_enabled")
	if err != nil || enabled != "true" {
		return
	}

	token, err := s.settingRepo.Get("slack_bot_token")
	if err != nil || token == "" {
		return
	}
	channel, err := s.settingRepo.Get("slack_channel")
	if err != nil || channel == "" {
		return
	}

	settingKey, message := s.mapEvent(evt)
	if settingKey == "" {
		return
	}

	// Check per-event toggle (defaults to true — enabled unless explicitly "false")
	val, _ := s.settingRepo.Get(settingKey)
	if val == "false" {
		return
	}

	client := slack.NewClient(token, channel)
	if err := client.Send(slack.Message{Text: message}); err != nil {
		slog.Warn("failed to send slack notification", "event", evt.Type, "error", err)
	}
}

func (s *NotificationService) mapEvent(evt scheduler.Event) (settingKey, message string) {
	switch evt.Type {
	case "job:updated":
		return s.mapJobEvent(evt)
	case "download:grabbed":
		return s.mapDownloadGrabbed(evt)
	case "download:updated":
		return s.mapDownloadUpdated(evt)
	default:
		return "", ""
	}
}

func (s *NotificationService) mapJobEvent(evt scheduler.Event) (string, string) {
	job, ok := evt.Data.(*model.Job)
	if !ok {
		return "", ""
	}
	if job.Status != model.JobStatusCompleted {
		return "", ""
	}

	switch job.Type {
	case model.JobTypeScan:
		return "slack_notify_scan_complete",
			fmt.Sprintf("Library scan completed — processed %d items", job.ProcessedItems)
	case model.JobTypeMetadataRefresh:
		return "slack_notify_metadata_refresh_complete",
			fmt.Sprintf("Metadata refresh completed — processed %d items", job.ProcessedItems)
	case model.JobTypePullListSearch:
		return "slack_notify_pull_list_search_complete",
			fmt.Sprintf("Pull list search completed — processed %d items", job.ProcessedItems)
	case model.JobTypeMissingSearch:
		if job.ProcessedItems == 0 {
			return "", "" // don't notify if nothing was found
		}
		return "slack_notify_missing_search_complete",
			fmt.Sprintf("Missing issue search completed — grabbed %d of %d issues", job.ProcessedItems, job.TotalItems)
	default:
		return "", ""
	}
}

func (s *NotificationService) mapDownloadGrabbed(evt scheduler.Event) (string, string) {
	item, ok := evt.Data.(*model.DownloadHistoryItem)
	if !ok {
		return "", ""
	}
	name := item.NZBName
	if item.SeriesTitle != "" && item.IssueNumber != "" {
		name = fmt.Sprintf("%s #%s", item.SeriesTitle, item.IssueNumber)
	}
	return "slack_notify_download_grabbed",
		fmt.Sprintf("Download grabbed: %s", name)
}

func (s *NotificationService) mapDownloadUpdated(evt scheduler.Event) (string, string) {
	data, ok := evt.Data.(map[string]interface{})
	if !ok {
		return "", ""
	}

	status, _ := data["status"].(string)
	idFloat, _ := data["id"].(float64)
	id := int64(idFloat)

	// Look up nzb_name for a richer message
	nzbName := "unknown"
	if id > 0 {
		if item, err := s.dlHistoryRepo.GetByID(id); err == nil && item != nil {
			if item.SeriesTitle != "" && item.IssueNumber != "" {
				nzbName = fmt.Sprintf("%s #%s", item.SeriesTitle, item.IssueNumber)
			} else {
				nzbName = item.NZBName
			}
		}
	}

	switch model.DownloadStatus(status) {
	case model.DownloadStatusCompleted:
		return "slack_notify_download_complete",
			fmt.Sprintf("Download completed: %s", nzbName)
	case model.DownloadStatusFailed:
		return "slack_notify_download_failed",
			fmt.Sprintf("Download failed: %s", nzbName)
	default:
		return "", ""
	}
}

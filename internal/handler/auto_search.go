package handler

import (
	"context"
	"log/slog"
	"time"

	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/service"
)

// triggerAutoSearch checks the auto_search_on_add setting and, if enabled,
// launches a background goroutine to search indexers and grab an NZB for the given issue.
func triggerAutoSearch(searchSvc *service.SearchService, settingRepo *repository.SettingRepo, issueID int64, label string) {
	enabled, _ := settingRepo.Get("auto_search_on_add")
	if enabled != "true" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		item, err := searchSvc.AutoSearchAndGrab(ctx, issueID)
		if err != nil {
			slog.Warn("auto-search failed", "issue_id", issueID, "label", label, "error", err)
			return
		}
		if item != nil {
			slog.Info("auto-search grabbed", "issue_id", issueID, "label", label, "nzb", item.NZBName)
		} else {
			slog.Info("auto-search found no results", "issue_id", issueID, "label", label)
		}
	}()
}

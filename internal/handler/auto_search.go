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
		outcome, err := searchSvc.AutoSearchAndGrab(ctx, issueID)
		if err != nil {
			slog.Warn("auto-search failed", "issue_id", issueID, "label", label, "error", err)
			return
		}
		if outcome != nil && outcome.Item != nil {
			slog.Info("auto-search grabbed", "issue_id", issueID, "label", label, "nzb", outcome.Item.NZBName)
		} else {
			reason := "no results"
			if outcome != nil && outcome.Reason != "" {
				reason = outcome.Reason
			}
			slog.Info("auto-search no grab", "issue_id", issueID, "label", label, "reason", reason)
		}
	}()
}

package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jeremy/longbox/internal/repository"
)

// PullListService handles the automated weekly pull list search and grab.
type PullListService struct {
	seriesRepo    *repository.SeriesRepo
	issueRepo     *repository.IssueRepo
	wantListRepo  *repository.WantListRepo
	dlHistoryRepo *repository.DownloadHistoryRepo
	searchSvc     *SearchService
	metaSvc       *MetadataService
}

func NewPullListService(
	seriesRepo *repository.SeriesRepo,
	issueRepo *repository.IssueRepo,
	wantListRepo *repository.WantListRepo,
	dlHistoryRepo *repository.DownloadHistoryRepo,
	searchSvc *SearchService,
	metaSvc *MetadataService,
) *PullListService {
	return &PullListService{
		seriesRepo:    seriesRepo,
		issueRepo:     issueRepo,
		wantListRepo:  wantListRepo,
		dlHistoryRepo: dlHistoryRepo,
		searchSvc:     searchSvc,
		metaSvc:       metaSvc,
	}
}

// PullListResult summarizes the outcome of a pull list search.
type PullListResult struct {
	IssuesSearched int `json:"issues_searched"`
	ResultsFound   int `json:"results_found"`
	Grabbed        int `json:"grabbed"`
	Skipped        int `json:"skipped"`
	Failed         int `json:"failed"`
}

// RunWeeklySearch is the main weekly job entry point.
func (s *PullListService) RunWeeklySearch(
	ctx context.Context,
	progress func(processed, total int, message string),
) (*PullListResult, error) {
	// Step 1a: Refresh tracked series metadata from ComicVine to pick up new issues
	if s.metaSvc != nil && s.metaSvc.HasAPIKey() {
		if progress != nil {
			progress(0, 0, "Refreshing tracked series from ComicVine...")
		}
		refreshed, failed, err := s.metaSvc.RefreshTrackedSeries(ctx, nil)
		if err != nil {
			slog.Warn("failed to refresh tracked series metadata", "error", err)
		} else {
			slog.Info("tracked series metadata refreshed before pull list search",
				"refreshed", refreshed,
				"failed", failed,
			)
		}
	}

	// Step 1b: Refresh the pull list so tracked series have their missing issues on the want list
	if err := s.RefreshPullList(); err != nil {
		slog.Warn("failed to refresh pull list", "error", err)
		// Continue anyway — the existing want list items are still valid
	}

	// Step 2: Load all want list items
	items, _, err := s.wantListRepo.List(1, 10000, "priority", "desc")
	if err != nil {
		return nil, fmt.Errorf("listing want list: %w", err)
	}

	// Step 3: Filter to items without an active download
	type searchTarget struct {
		issueID int64
		label   string
	}
	var targets []searchTarget

	for _, item := range items {
		exists, err := s.dlHistoryRepo.ExistsForIssue(item.IssueID)
		if err != nil {
			slog.Warn("error checking download history", "issue_id", item.IssueID, "error", err)
			continue
		}
		if exists {
			continue
		}
		label := fmt.Sprintf("%s #%s", item.SeriesTitle, item.IssueNumber)
		targets = append(targets, searchTarget{issueID: item.IssueID, label: label})
	}

	result := &PullListResult{
		IssuesSearched: len(targets),
	}
	total := len(targets)

	if total == 0 {
		if progress != nil {
			progress(0, 0, "No wanted issues to search for")
		}
		return result, nil
	}

	slog.Info("starting pull list search", "targets", total)

	// Step 4: Search and grab each target
	for i, target := range targets {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		if progress != nil {
			progress(i, total, fmt.Sprintf("Searching for %s", target.label))
		}

		grabbed, err := s.searchSvc.AutoSearchAndGrab(ctx, target.issueID)
		if err != nil {
			slog.Warn("auto-grab failed",
				"issue_id", target.issueID,
				"label", target.label,
				"error", err,
			)
			result.Failed++
			continue
		}

		if grabbed != nil {
			result.Grabbed++
			result.ResultsFound++
			slog.Info("auto-grabbed issue",
				"label", target.label,
				"nzb", grabbed.NZBName,
			)
		} else {
			result.Skipped++
		}
	}

	if progress != nil {
		progress(total, total, "Pull list search complete")
	}

	slog.Info("pull list search complete",
		"searched", result.IssuesSearched,
		"found", result.ResultsFound,
		"grabbed", result.Grabbed,
		"skipped", result.Skipped,
		"failed", result.Failed,
	)

	return result, nil
}

// SearchMissing is a lightweight periodic search that refreshes the pull list
// (DB-only, no ComicVine API calls) and searches for all wanted issues.
// Designed to run frequently (e.g. every 10 minutes) to fill gaps as NZBs appear.
func (s *PullListService) SearchMissing(
	ctx context.Context,
	progress func(processed, total int, message string),
) (*PullListResult, error) {
	// Step 1: Refresh the pull list (DB-only — adds missing issues to want list)
	if progress != nil {
		progress(0, 0, "Refreshing pull list...")
	}
	if err := s.RefreshPullList(); err != nil {
		slog.Warn("failed to refresh pull list", "error", err)
	}

	// Step 2: Load all want list items
	items, _, err := s.wantListRepo.List(1, 10000, "priority", "desc")
	if err != nil {
		return nil, fmt.Errorf("listing want list: %w", err)
	}

	// Step 3: Filter to items without an active download
	type searchTarget struct {
		issueID int64
		label   string
	}
	var targets []searchTarget

	for _, item := range items {
		exists, err := s.dlHistoryRepo.ExistsForIssue(item.IssueID)
		if err != nil {
			slog.Warn("error checking download history", "issue_id", item.IssueID, "error", err)
			continue
		}
		if exists {
			continue
		}
		label := fmt.Sprintf("%s #%s", item.SeriesTitle, item.IssueNumber)
		targets = append(targets, searchTarget{issueID: item.IssueID, label: label})
	}

	result := &PullListResult{
		IssuesSearched: len(targets),
	}
	total := len(targets)

	if total == 0 {
		if progress != nil {
			progress(0, 0, "No wanted issues to search for")
		}
		return result, nil
	}

	slog.Info("starting missing issue search", "targets", total)

	// Step 4: Search and grab each target
	for i, target := range targets {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		if progress != nil {
			progress(i, total, fmt.Sprintf("Searching for %s", target.label))
		}

		grabbed, err := s.searchSvc.AutoSearchAndGrab(ctx, target.issueID)
		if err != nil {
			slog.Warn("auto-grab failed",
				"issue_id", target.issueID,
				"label", target.label,
				"error", err,
			)
			result.Failed++
			continue
		}

		if grabbed != nil {
			result.Grabbed++
			result.ResultsFound++
			slog.Info("auto-grabbed missing issue",
				"label", target.label,
				"nzb", grabbed.NZBName,
			)
		} else {
			result.Skipped++
		}
	}

	if progress != nil {
		progress(total, total, "Missing issue search complete")
	}

	slog.Info("missing issue search complete",
		"searched", result.IssuesSearched,
		"found", result.ResultsFound,
		"grabbed", result.Grabbed,
		"skipped", result.Skipped,
		"failed", result.Failed,
	)

	return result, nil
}

// RefreshPullList ensures all tracked series have their missing issues on the want list,
// and removes want list items for issues that now have files in the library.
func (s *PullListService) RefreshPullList() error {
	// First, remove want list items for issues that now have files
	removed, err := s.wantListRepo.RemoveFulfilled()
	if err != nil {
		slog.Warn("failed to remove fulfilled want list items", "error", err)
	} else if removed > 0 {
		slog.Info("removed fulfilled issues from want list", "count", removed)
	}

	// Then add missing issues for all tracked series
	tracked, err := s.seriesRepo.ListTracked()
	if err != nil {
		return fmt.Errorf("listing tracked series: %w", err)
	}

	for _, series := range tracked {
		if _, err := s.wantListRepo.AddMissingForSeries(series.ID); err != nil {
			slog.Warn("failed to add missing issues for series",
				"series_id", series.ID,
				"title", series.Title,
				"error", err,
			)
		}
	}

	slog.Info("pull list refreshed", "tracked_series", len(tracked))
	return nil
}

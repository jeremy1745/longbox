package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scheduler"
	"github.com/jeremy/longbox/internal/service"
)

type CalendarHandler struct {
	issueRepo    *repository.IssueRepo
	seriesRepo   *repository.SeriesRepo
	wantListRepo *repository.WantListRepo
	metaSvc      *service.MetadataService
	sched        *scheduler.Scheduler
	searchSvc    *service.SearchService
	settingRepo  *repository.SettingRepo
}

func NewCalendarHandler(
	issueRepo *repository.IssueRepo,
	seriesRepo *repository.SeriesRepo,
	wantListRepo *repository.WantListRepo,
	metaSvc *service.MetadataService,
	sched *scheduler.Scheduler,
	searchSvc *service.SearchService,
	settingRepo *repository.SettingRepo,
) *CalendarHandler {
	return &CalendarHandler{
		issueRepo:    issueRepo,
		seriesRepo:   seriesRepo,
		wantListRepo: wantListRepo,
		metaSvc:      metaSvc,
		sched:        sched,
		searchSvc:    searchSvc,
		settingRepo:  settingRepo,
	}
}

// Upcoming returns issues with store_date in the given date range from the LOCAL database.
// Query params: start (YYYY-MM-DD), end (YYYY-MM-DD), tracked_only (bool)
func (h *CalendarHandler) Upcoming(w http.ResponseWriter, r *http.Request) {
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")
	trackedOnly := r.URL.Query().Get("tracked_only") == "true"

	if start == "" || end == "" {
		writeError(w, http.StatusBadRequest, "MISSING_DATES", "start and end query params required (YYYY-MM-DD)")
		return
	}

	issues, err := h.issueRepo.ListByDateRange(start, end, trackedOnly)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "CALENDAR_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"issues": issues,
		"total":  len(issues),
	})
}

// Releases fetches ALL comics releasing in a date range from ComicVine,
// cross-referenced with local ownership and tracking data.
// Query params: start (YYYY-MM-DD), end (YYYY-MM-DD)
//
// Bounded by a 30s request timeout so the handler can't pin the calling
// page on "Loading…" forever — when a library scan is holding the CV
// rate limiter, this used to wait for the full hourly reset window
// before returning. Now it returns 504 with a clear message and the
// frontend renders an inline error instead of a permanent spinner.
func (h *CalendarHandler) Releases(w http.ResponseWriter, r *http.Request) {
	start := r.URL.Query().Get("start")
	end := r.URL.Query().Get("end")

	if start == "" || end == "" {
		writeError(w, http.StatusBadRequest, "MISSING_DATES", "start and end query params required (YYYY-MM-DD)")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	releases, debug, err := h.metaSvc.GetWeeklyReleases(ctx, start, end)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusGatewayTimeout, "RELEASES_TIMEOUT",
				"upstream release feed didn't respond in time — a library scan or CV rate-limit is in flight; try again in a minute")
			return
		}
		writeError(w, http.StatusInternalServerError, "RELEASES_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"releases": releases,
		"total":    len(releases),
		"debug":    debug,
	})
}

// RefreshTracked triggers a background job to refresh metadata for all tracked series.
func (h *CalendarHandler) RefreshTracked(w http.ResponseWriter, r *http.Request) {
	tracked, err := h.seriesRepo.ListTracked()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_TRACKED_FAILED", err.Error())
		return
	}

	if len(tracked) == 0 {
		writeError(w, http.StatusBadRequest, "NO_TRACKED_SERIES",
			"No tracked series found. Track series from the Library page first, then match them to ComicVine.")
		return
	}

	matchedCount := 0
	for _, s := range tracked {
		if s.ComicVineID != nil {
			matchedCount++
		}
	}

	if matchedCount == 0 {
		writeError(w, http.StatusBadRequest, "NO_MATCHED_SERIES",
			"Tracked series exist but none are matched to ComicVine. Match series from the Library page first.")
		return
	}

	job, err := h.sched.Submit(model.JobTypeMetadataRefresh)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "JOB_SUBMIT_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"job_id":         job.ID,
		"tracked_series": len(tracked),
		"matched_series": matchedCount,
		"message":        "Refreshing tracked series metadata from ComicVine",
	})
}

// TrackSeries tracks a series from the pull list by its ComicVine volume ID.
// This creates a local series (if needed), populates issues, and marks it tracked.
// Body: { "series_cv_id": 12345 }
func (h *CalendarHandler) TrackSeries(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SeriesCVID int  `json:"series_cv_id"`
		WantAll    *bool `json:"want_all,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if body.SeriesCVID == 0 {
		writeError(w, http.StatusBadRequest, "MISSING_CV_ID", "series_cv_id is required")
		return
	}

	// Default to NOT auto-wanting back issues. Tracking a series should follow
	// new releases, not silently dump every prior issue into the want list.
	// Callers can opt in by passing "want_all": true.
	wantAll := false
	if body.WantAll != nil {
		wantAll = *body.WantAll
	}

	series, wantAdded, err := h.metaSvc.TrackFromComicVine(body.SeriesCVID, h.wantListRepo, wantAll)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TRACK_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"series":          series,
		"tracked":         true,
		"want_list_added": wantAdded,
	})
}

// WantIssue adds an issue to the want list.
// Supports three paths:
//   - By local issue ID: { "local_issue_id": 42 }
//   - By ComicVine issue ID: { "comicvine_id": 67890, "series_cv_id": 12345 }
//   - By series CV ID + issue number: { "series_cv_id": 12345, "issue_number": "5" }
func (h *CalendarHandler) WantIssue(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ComicVineID  int    `json:"comicvine_id"`
		SeriesCVID   int    `json:"series_cv_id"`
		LocalIssueID int64  `json:"local_issue_id"`
		IssueNumber  string `json:"issue_number"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	// Path 1: Want by local issue ID (already exists in DB)
	if body.LocalIssueID > 0 {
		item, err := h.wantListRepo.Create(body.LocalIssueID, 0, "")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "WANT_FAILED", err.Error())
			return
		}
		triggerAutoSearch(h.searchSvc, h.settingRepo, body.LocalIssueID, fmt.Sprintf("calendar-want issue %d", body.LocalIssueID))
		writeJSON(w, http.StatusCreated, item)
		return
	}

	// Path 2: Want by ComicVine issue ID (may need to create series/issue first)
	if body.ComicVineID > 0 {
		item, err := h.metaSvc.WantIssueFromComicVine(body.ComicVineID, body.SeriesCVID, h.wantListRepo)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "WANT_FAILED", err.Error())
			return
		}
		triggerAutoSearch(h.searchSvc, h.settingRepo, item.IssueID, fmt.Sprintf("calendar-want cv-issue %d", body.ComicVineID))
		writeJSON(w, http.StatusCreated, item)
		return
	}

	// Path 3: Want by series CV ID + issue number (for future releases without issue-level CV ID)
	if body.SeriesCVID > 0 && body.IssueNumber != "" {
		item, err := h.metaSvc.WantIssueBySeriesAndNumber(body.SeriesCVID, body.IssueNumber, h.wantListRepo)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "WANT_FAILED", err.Error())
			return
		}
		triggerAutoSearch(h.searchSvc, h.settingRepo, item.IssueID, fmt.Sprintf("calendar-want series %d #%s", body.SeriesCVID, body.IssueNumber))
		writeJSON(w, http.StatusCreated, item)
		return
	}

	writeError(w, http.StatusBadRequest, "MISSING_ID", "comicvine_id, local_issue_id, or series_cv_id + issue_number is required")
}

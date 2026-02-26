package handler

import (
	"net/http"

	"github.com/jeremy/longbox/internal/repository"
)

type CalendarHandler struct {
	issueRepo *repository.IssueRepo
}

func NewCalendarHandler(issueRepo *repository.IssueRepo) *CalendarHandler {
	return &CalendarHandler{issueRepo: issueRepo}
}

// Upcoming returns issues with store_date in the given date range.
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

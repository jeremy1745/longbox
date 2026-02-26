package handler

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scheduler"
)

type JobHandler struct {
	jobRepo   *repository.JobRepo
	scheduler *scheduler.Scheduler
	eventBus  *scheduler.EventBus
}

func NewJobHandler(jobRepo *repository.JobRepo, sched *scheduler.Scheduler, eventBus *scheduler.EventBus) *JobHandler {
	return &JobHandler{
		jobRepo:   jobRepo,
		scheduler: sched,
		eventBus:  eventBus,
	}
}

// List returns recent jobs.
// GET /api/v1/jobs?limit=50
func (h *JobHandler) List(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}

	jobs, err := h.jobRepo.List(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "JOB_LIST_FAILED", err.Error())
		return
	}
	if jobs == nil {
		jobs = []model.Job{}
	}

	active, _ := h.jobRepo.ActiveJobs()
	if active == nil {
		active = []model.Job{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"jobs":   jobs,
		"active": active,
	})
}

// Get returns a single job by ID.
// GET /api/v1/jobs/{id}
func (h *JobHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid job ID")
		return
	}

	job, err := h.jobRepo.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "JOB_GET_FAILED", err.Error())
		return
	}
	if job == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "job not found")
		return
	}

	writeJSON(w, http.StatusOK, job)
}

// Cancel stops a running job.
// POST /api/v1/jobs/{id}/cancel
func (h *JobHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid job ID")
		return
	}

	if err := h.scheduler.Cancel(id); err != nil {
		writeError(w, http.StatusBadRequest, "CANCEL_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// Events provides an SSE stream of real-time job updates.
// GET /api/v1/events
func (h *JobHandler) Events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "SSE_UNSUPPORTED", "streaming not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Subscribe to events
	ch := h.eventBus.Subscribe()
	defer h.eventBus.Unsubscribe(ch)

	slog.Debug("SSE client connected", "clients", h.eventBus.ClientCount())

	// Send a connection-established event
	fmt.Fprintf(w, "data: {\"type\":\"connected\"}\n\n")
	flusher.Flush()

	// Stream events until client disconnects
	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			slog.Debug("SSE client disconnected")
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			data, err := scheduler.FormatSSE(evt)
			if err != nil {
				slog.Warn("failed to format SSE event", "error", err)
				continue
			}
			w.Write(data)
			flusher.Flush()
		}
	}
}

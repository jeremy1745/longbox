package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/newznab"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scheduler"
	"github.com/jeremy/longbox/internal/service"
	"github.com/jeremy/longbox/internal/template"
)

type LibraryHandler struct {
	librarySvc    *service.LibraryService
	fileRepo      *repository.FileRepo
	seriesRepo    *repository.SeriesRepo
	issueRepo     *repository.IssueRepo
	scheduler     *scheduler.Scheduler
	settingRepo   *repository.SettingRepo
	backlogRepo   *repository.BacklogRepo
	indexerRepo   *repository.IndexerRepo
	dlClientRepo  *repository.DownloadClientRepo
	wantListRepo  *repository.WantListRepo
	organizeSvc   *service.FileOrganizerService
}

func NewLibraryHandler(
	librarySvc *service.LibraryService,
	fileRepo *repository.FileRepo,
	seriesRepo *repository.SeriesRepo,
	issueRepo *repository.IssueRepo,
	sched *scheduler.Scheduler,
	settingRepo *repository.SettingRepo,
	backlogRepo *repository.BacklogRepo,
	indexerRepo *repository.IndexerRepo,
	dlClientRepo *repository.DownloadClientRepo,
	wantListRepo *repository.WantListRepo,
	organizeSvc *service.FileOrganizerService,
) *LibraryHandler {
	return &LibraryHandler{
		librarySvc:   librarySvc,
		fileRepo:     fileRepo,
		seriesRepo:   seriesRepo,
		issueRepo:    issueRepo,
		scheduler:    sched,
		settingRepo:  settingRepo,
		backlogRepo:  backlogRepo,
		indexerRepo:  indexerRepo,
		dlClientRepo: dlClientRepo,
		wantListRepo: wantListRepo,
		organizeSvc:  organizeSvc,
	}
}

// TrashOrphanFiles trashes every comic_files row whose issue_id is NULL,
// removing both the on-disk file (to OS recycle bin) and the DB row.
// Used to clean up persistent orphans that the reattach pass can't
// reconnect to a valid issue.
//
// POST /api/v1/admin/trash-orphan-files          — applies trash + delete
// POST /api/v1/admin/trash-orphan-files?dry=1    — preview only
func (h *LibraryHandler) TrashOrphanFiles(w http.ResponseWriter, r *http.Request) {
	dry := r.URL.Query().Get("dry") == "1"
	res, err := h.librarySvc.TrashOrphanFiles(r.Context(), dry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TRASH_ORPHANS_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// ReattachOrphanFiles relinks comic_files rows whose issue_id is NULL.
// Synchronous endpoint — bounded by the request timeout. Bigger libraries
// would need a background-job variant; for the user's case (≤100 orphans
// expected) this is fine.
// POST /api/v1/admin/reattach-orphans
func (h *LibraryHandler) ReattachOrphanFiles(w http.ResponseWriter, r *http.Request) {
	res, err := h.librarySvc.ReattachOrphanFiles(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "REATTACH_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// AdoptStrandedFolders walks each top-level subfolder of the library
// looking for SAB-style download folders that contain comic files but
// don't follow the canonical layout. The folder name is parsed for series
// + issue + year; the inside files are reassigned in the DB so a
// subsequent Reorganize moves them to "<Series> (<Year>)/<Series>
// (<Year>) NNN.<ext>" and cleans up the empty release folder. Submits as
// a scheduler job so progress shows in the active-job banner / Jobs page.
//
// POST /api/v1/admin/adopt-folders
func (h *LibraryHandler) AdoptStrandedFolders(w http.ResponseWriter, r *http.Request) {
	job, err := h.scheduler.Submit(model.JobTypeAdoptFolders)
	if err != nil {
		writeError(w, http.StatusConflict, "ADOPT_BUSY", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"job_id":  job.ID,
		"status":  job.Status,
		"message": "Adopt stranded folders submitted — track on Jobs page or active-job banner. Run Reorganize after to move files into canonical folders.",
	})
}

// ReorganizeLibrary runs the canonical naming template across every
// comic_files row, moving each file to its computed path and cleaning up
// emptied source directories. Forces the saved template back to the
// LongBox default ("<series> (<year>)/<series> (<year>) NNN.<ext>") so a
// user who customized it earlier still gets migrated to the canonical
// shape. Pass ?dry=1 for a preview that touches no files.
//
// POST /api/v1/admin/reorganize          — runs for real
// POST /api/v1/admin/reorganize?dry=1    — preview only
func (h *LibraryHandler) ReorganizeLibrary(w http.ResponseWriter, r *http.Request) {
	dry := r.URL.Query().Get("dry") == "1"

	if !dry {
		// Force-reset the saved template before executing so an old custom
		// pattern doesn't override the canonical default.
		if err := h.organizeSvc.SetTemplate(template.DefaultTemplate); err != nil {
			writeError(w, http.StatusInternalServerError, "TEMPLATE_RESET_FAILED", err.Error())
			return
		}
	}

	libraryDir := h.librarySvc.GetLibraryDir()
	if dry {
		previews, err := h.organizeSvc.Preview(libraryDir, template.DefaultTemplate)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "PREVIEW_FAILED", err.Error())
			return
		}
		// Bucket by status for the summary, but only ship rows the UI
		// actually renders (move + conflict). The "skip" rows balloon the
		// response — a 5k-file library produces a 2.4MB JSON payload that
		// spends most of its bytes on rows the user can't act on.
		var moves, conflicts, skips int
		actionable := make([]service.RenamePreview, 0)
		for _, p := range previews {
			switch p.Status {
			case "move":
				moves++
				actionable = append(actionable, p)
			case "conflict":
				conflicts++
				actionable = append(actionable, p)
			default:
				skips++
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"dry_run":   true,
			"moves":     moves,
			"conflicts": conflicts,
			"skipped":   skips,
			"previews":  actionable,
		})
		return
	}

	// Submit as a scheduled job so the move runs in the background, the
	// active-job banner picks it up, and the user can navigate away (or
	// close the tab) without killing the operation. Sync execution would
	// pin the user on Settings while thousands of files move and would
	// abandon the partial result the moment they navigated away.
	job, err := h.scheduler.Submit(model.JobTypeReorganize)
	if err != nil {
		writeError(w, http.StatusConflict, "REORGANIZE_BUSY", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"job_id":  job.ID,
		"status":  job.Status,
		"message": "Reorganize submitted — track progress on the Jobs page or in the active-job banner.",
	})
}

// TestSearch runs a raw query against every enabled indexer and returns the
// unfiltered response so the user can compare what LongBox sees vs what they
// see in Prowlarr's UI. Useful for diagnosing "no indexer hits" backlog
// failures — paste the exact query LongBox is sending and inspect what comes
// back. GET /api/v1/admin/test-search?q=...&cat=...
func (h *LibraryHandler) TestSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "MISSING_QUERY", "q parameter is required")
		return
	}
	catOverride := strings.TrimSpace(r.URL.Query().Get("cat"))

	indexers, err := h.indexerRepo.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INDEXER_LIST_FAILED", err.Error())
		return
	}

	type indexerTrace struct {
		IndexerID    int64                 `json:"indexer_id"`
		IndexerName  string                `json:"indexer_name"`
		IndexerType  string                `json:"indexer_type"`
		IndexerURL   string                `json:"indexer_url"`
		Categories   string                `json:"categories_sent"`
		Enabled      bool                  `json:"enabled"`
		ResultCount  int                   `json:"result_count"`
		Error        string                `json:"error,omitempty"`
		TopResults   []map[string]any      `json:"top_results,omitempty"`
	}

	traces := make([]indexerTrace, 0, len(indexers))
	for _, idx := range indexers {
		t := indexerTrace{
			IndexerID:   idx.ID,
			IndexerName: idx.Name,
			IndexerType: string(idx.Type),
			IndexerURL:  idx.URL,
			Categories:  idx.Categories,
			Enabled:     idx.Enabled,
		}
		if !idx.Enabled {
			t.Error = "indexer disabled"
			traces = append(traces, t)
			continue
		}
		isProwlarr := idx.Type == model.IndexerTypeProwlarr
		client := newznab.NewClient(idx.URL, idx.APIKey, isProwlarr)
		cats := idx.Categories
		if catOverride != "" {
			cats = catOverride
			t.Categories = catOverride
		}
		results, err := client.Search(q, strings.Split(cats, ","))
		if err != nil {
			t.Error = err.Error()
			traces = append(traces, t)
			continue
		}
		t.ResultCount = len(results)
		max := len(results)
		if max > 10 {
			max = 10
		}
		for i := 0; i < max; i++ {
			r := results[i]
			t.TopResults = append(t.TopResults, map[string]any{
				"title":    r.Title,
				"size":     r.Size,
				"grabs":    r.Grabs,
				"category": r.Category,
				"guid":     r.GUID,
			})
		}
		traces = append(traces, t)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"query":    q,
		"indexers": traces,
	})
}

// ReconcileBacklog marks every non-terminal backlog item whose underlying
// issue is already owned (file on disk) or already-grabbed (completed
// download history row) as 'completed'. Fixes piles of bogus "failed
// (no nzb found)" rows for issues you actually own.
// POST /api/v1/admin/reconcile-backlog
func (h *LibraryHandler) ReconcileBacklog(w http.ResponseWriter, r *http.Request) {
	n, err := h.backlogRepo.ReconcileOwned()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RECONCILE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reconciled": n})
}

// PipelineStatus surfaces the state of every moving part of the
// "discovery → search → grab → import" pipeline so a user can see at a
// glance why nothing is happening. Always returns 200 — fields are nullable.
// GET /api/v1/admin/pipeline-status
func (h *LibraryHandler) PipelineStatus(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{}

	// Cron toggles + last-run timestamps
	cron := map[string]any{}
	for _, k := range []string{
		"missing_search_enabled", "missing_search_interval", "missing_search_last_run",
		"pull_list_enabled", "pull_list_day", "pull_list_hour", "pull_list_last_run",
		"auto_scan_enabled", "auto_scan_interval", "auto_scan_last_run",
	} {
		v, _ := h.settingRepo.Get(k)
		cron[k] = v
	}
	resp["cron"] = cron

	// Backlog item breakdown
	if breakdown, err := h.backlogRepo.StatusBreakdown(); err == nil {
		resp["backlog_status"] = breakdown
	} else {
		resp["backlog_status_error"] = err.Error()
	}
	if recent, err := h.backlogRepo.LastFailures(10); err == nil {
		resp["recent_failures"] = recent
	}

	// Indexer + download client visibility
	if indexers, err := h.indexerRepo.List(); err == nil {
		summary := make([]map[string]any, 0, len(indexers))
		for _, ix := range indexers {
			summary = append(summary, map[string]any{
				"id":      ix.ID,
				"name":    ix.Name,
				"enabled": ix.Enabled,
				"type":    ix.Type,
			})
		}
		resp["indexers"] = summary
	}
	if clients, err := h.dlClientRepo.List(); err == nil {
		summary := make([]map[string]any, 0, len(clients))
		for _, dc := range clients {
			summary = append(summary, map[string]any{
				"id":      dc.ID,
				"name":    dc.Name,
				"enabled": dc.Enabled,
				"type":    dc.Type,
				"url":     dc.URL,
			})
		}
		resp["download_clients"] = summary
	}

	// Want list count
	if _, total, err := h.wantListRepo.List(1, 1, "", ""); err == nil {
		resp["want_list_total"] = total
	}

	// Server time so the client can compare cron last-run timestamps
	resp["server_time"] = time.Now().UTC().Format(time.RFC3339)

	writeJSON(w, http.StatusOK, resp)
}

// NewThisWeek returns owned issues whose store_date falls in the current
// comic shipping week (Wed→Tue) by default. Pass ?days=N to override the
// window length (counts backwards from today, inclusive).
// GET /api/v1/library/new-this-week
func (h *LibraryHandler) NewThisWeek(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	var start time.Time
	if d := r.URL.Query().Get("days"); d != "" {
		days, err := strconv.Atoi(d)
		if err != nil || days < 1 || days > 60 {
			writeError(w, http.StatusBadRequest, "INVALID_DAYS", "days must be 1..60")
			return
		}
		start = startOfDay(now.AddDate(0, 0, -(days - 1)))
	} else {
		start = startOfComicWeek(now)
	}
	end := endOfDay(now)

	issues, err := h.issueRepo.ListOwnedByDateRange(
		start.Format("2006-01-02"),
		end.Format("2006-01-02"),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"issues":     issues,
		"total":      len(issues),
		"start_date": start.Format("2006-01-02"),
		"end_date":   end.Format("2006-01-02"),
	})
}

// startOfComicWeek returns midnight of the most recent Wednesday at or before
// t. Wednesday is the conventional new-comic-day in US distribution.
func startOfComicWeek(t time.Time) time.Time {
	d := t.Weekday() // Sunday=0..Saturday=6
	// days since the most recent Wednesday (3)
	delta := (int(d) - 3 + 7) % 7
	return startOfDay(t.AddDate(0, 0, -delta))
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func endOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, t.Location())
}

// DedupeSeries merges series rows that share the same normalized title +
// year into a single canonical row. Canonical preference: has comicvine_id,
// then metron_id, then most files, then lowest id. The duplicates have their
// issues and files reassigned via the existing MergeSeriesInto flow.
// POST /api/v1/admin/dedupe-series
func (h *LibraryHandler) DedupeSeries(w http.ResponseWriter, r *http.Request) {
	result, err := h.librarySvc.DedupeSeries()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DEDUPE_SERIES_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// DedupeFiles finds comic_files rows attached to the same issue and trashes
// all but a canonical (preferred: ComicInfo.xml present, CBZ, largest size).
// POST /api/v1/admin/dedupe-files       — applies trash + DB deletes
// POST /api/v1/admin/dedupe-files?dry=1 — preview only, files untouched
func (h *LibraryHandler) DedupeFiles(w http.ResponseWriter, r *http.Request) {
	dry := r.URL.Query().Get("dry") == "1"
	result, err := h.librarySvc.DedupeFiles(dry)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DEDUPE_FILES_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// DedupeIssues merges duplicate (series_id, issue_number) issue rows into a
// canonical row and removes the duplicates. Reassigns comic_files / download_history
// / backlog_items, copies want_list / story_arc_issues to the canonical, and
// deletes the dupes.
// POST /api/v1/admin/dedupe-issues
func (h *LibraryHandler) DedupeIssues(w http.ResponseWriter, r *http.Request) {
	result, err := h.librarySvc.DedupeIssues()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "DEDUPE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// PruneWantList removes every want_list row whose issue now has a linked
// comic_files row — fulfilling the "I already own this" condition.
// POST /api/v1/admin/prune-want-list
func (h *LibraryHandler) PruneWantList(w http.ResponseWriter, r *http.Request) {
	count, err := h.librarySvc.PruneFulfilledWantList()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PRUNE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": count})
}

// BackfillReadStatus promotes every issue currently flagged "reading" whose
// last_read_page is at or past the end of its file to "read".
// POST /api/v1/admin/backfill-read-status
func (h *LibraryHandler) BackfillReadStatus(w http.ResponseWriter, r *http.Request) {
	count, err := h.issueRepo.BackfillReadStatusFromProgress()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "BACKFILL_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"promoted": count})
}

// Scan triggers a background library scan and returns the job immediately.
func (h *LibraryHandler) Scan(w http.ResponseWriter, r *http.Request) {
	job, err := h.scheduler.Submit(model.JobTypeScan)
	if err != nil {
		writeError(w, http.StatusConflict, "SCAN_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

// ScanForceCV triggers a background scan that ignores the per-series CV
// refresh TTL — every tracked series with a ComicVine match is re-fetched.
func (h *LibraryHandler) ScanForceCV(w http.ResponseWriter, r *http.Request) {
	job, err := h.scheduler.Submit(model.JobTypeScanForceCV)
	if err != nil {
		writeError(w, http.StatusConflict, "SCAN_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (h *LibraryHandler) Stats(w http.ResponseWriter, r *http.Request) {
	_, totalFiles, err := h.fileRepo.List(1, 1)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STATS_FAILED", err.Error())
		return
	}

	_, totalSeries, err := h.seriesRepo.List(1, 1, "", "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STATS_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_files":  totalFiles,
		"total_series": totalSeries,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

// writeInternalError logs the detailed error but returns a generic message to the client.
func writeInternalError(w http.ResponseWriter, code string, err error) {
	slog.Error("internal error", "code", code, "error", err)
	writeError(w, http.StatusInternalServerError, code, "an internal error occurred")
}

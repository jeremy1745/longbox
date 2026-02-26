package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/scanner"
	"github.com/jeremy/longbox/internal/scheduler"
	"github.com/jeremy/longbox/internal/service"
)

type SettingsHandler struct {
	metaSvc    *service.MetadataService
	librarySvc *service.LibraryService
	watcher    *scanner.Watcher
	scheduler  *scheduler.Scheduler
}

func NewSettingsHandler(
	metaSvc *service.MetadataService,
	librarySvc *service.LibraryService,
	watcher *scanner.Watcher,
	sched *scheduler.Scheduler,
) *SettingsHandler {
	return &SettingsHandler{
		metaSvc:    metaSvc,
		librarySvc: librarySvc,
		watcher:    watcher,
		scheduler:  sched,
	}
}

// GetSettings returns current application settings.
// GET /api/v1/settings
func (h *SettingsHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	settings := h.metaSvc.GetSettings()
	writeJSON(w, http.StatusOK, settings)
}

// UpdateAPIKey saves a new ComicVine API key.
// PUT /api/v1/settings/comicvine-api-key
// Body: {"api_key": "your-key-here"}
func (h *SettingsHandler) UpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		APIKey string `json:"api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if req.APIKey == "" {
		writeError(w, http.StatusBadRequest, "MISSING_KEY", "api_key is required")
		return
	}

	if err := h.metaSvc.SetAPIKey(req.APIKey); err != nil {
		writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":                   "updated",
		"comicvine_api_key_masked": h.metaSvc.GetAPIKeyMasked(),
		"comicvine_api_key_source": "settings",
		"comicvine_api_key_set":    true,
	})
}

// TestAPIKey tests the ComicVine API key by making a simple search.
// POST /api/v1/settings/comicvine-api-key/test
func (h *SettingsHandler) TestAPIKey(w http.ResponseWriter, r *http.Request) {
	if !h.metaSvc.HasAPIKey() {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"valid":   false,
			"message": "No API key configured",
		})
		return
	}

	// Try a simple search to verify the key works
	_, _, err := h.metaSvc.SearchVolumes("Batman", 1)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"valid":   false,
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"valid":            true,
		"message":          "API key is valid",
		"hourly_remaining": h.metaSvc.HourlyRemaining(),
	})
}

// UpdateLibraryDir saves a new library directory and triggers a scan.
// PUT /api/v1/settings/library-dir
// Body: {"library_dir": "/path/to/comics"}
func (h *SettingsHandler) UpdateLibraryDir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LibraryDir string `json:"library_dir"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	dir := strings.TrimSpace(req.LibraryDir)
	if dir == "" {
		writeError(w, http.StatusBadRequest, "MISSING_DIR", "library_dir is required")
		return
	}

	// Expand ~ to home directory
	if strings.HasPrefix(dir, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			dir = filepath.Join(home, dir[2:])
		}
	}

	// Validate directory exists
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusBadRequest, "DIR_NOT_FOUND",
				fmt.Sprintf("directory does not exist: %s", dir))
		} else {
			writeError(w, http.StatusBadRequest, "DIR_ERROR",
				fmt.Sprintf("cannot access directory: %v", err))
		}
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, "NOT_A_DIR",
			fmt.Sprintf("path is not a directory: %s", dir))
		return
	}

	// Persist to settings DB
	if err := h.metaSvc.SetLibraryDir(dir); err != nil {
		writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
		return
	}

	// Update LibraryService runtime directory
	h.librarySvc.SetLibraryDir(dir)

	// Restart the file watcher for the new directory
	if h.watcher != nil {
		if err := h.watcher.Restart(dir); err != nil {
			slog.Error("failed to restart file watcher", "dir", dir, "error", err)
		}
	}

	// Trigger a scan of the new directory
	job, err := h.scheduler.Submit(model.JobTypeScan)
	if err != nil {
		slog.Warn("failed to auto-scan after dir change", "error", err)
	}

	response := map[string]interface{}{
		"status":      "updated",
		"library_dir": dir,
	}
	if job != nil {
		response["scan_job_id"] = job.ID
	}

	writeJSON(w, http.StatusOK, response)
}

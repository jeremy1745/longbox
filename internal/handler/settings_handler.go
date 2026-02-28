package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scanner"
	"github.com/jeremy/longbox/internal/scheduler"
	"github.com/jeremy/longbox/internal/service"
	"github.com/jeremy/longbox/internal/slack"
)

type SettingsHandler struct {
	metaSvc     *service.MetadataService
	librarySvc  *service.LibraryService
	watcher     *scanner.Watcher
	scheduler   *scheduler.Scheduler
	settingRepo *repository.SettingRepo
}

func NewSettingsHandler(
	metaSvc *service.MetadataService,
	librarySvc *service.LibraryService,
	watcher *scanner.Watcher,
	sched *scheduler.Scheduler,
	settingRepo *repository.SettingRepo,
) *SettingsHandler {
	return &SettingsHandler{
		metaSvc:     metaSvc,
		librarySvc:  librarySvc,
		watcher:     watcher,
		scheduler:   sched,
		settingRepo: settingRepo,
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

// UpdatePullListSchedule saves pull list automation settings.
// PUT /api/v1/settings/pull-list-schedule
func (h *SettingsHandler) UpdatePullListSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled *bool `json:"enabled"`
		Day     *int  `json:"day"`
		Hour    *int  `json:"hour"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if req.Enabled != nil {
		val := "false"
		if *req.Enabled {
			val = "true"
		}
		if err := h.settingRepo.Set("pull_list_enabled", val); err != nil {
			writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
			return
		}
	}

	if req.Day != nil {
		if *req.Day < 0 || *req.Day > 6 {
			writeError(w, http.StatusBadRequest, "INVALID_DAY", "day must be 0-6 (Sunday-Saturday)")
			return
		}
		if err := h.settingRepo.Set("pull_list_day", strconv.Itoa(*req.Day)); err != nil {
			writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
			return
		}
	}

	if req.Hour != nil {
		if *req.Hour < 0 || *req.Hour > 23 {
			writeError(w, http.StatusBadRequest, "INVALID_HOUR", "hour must be 0-23")
			return
		}
		if err := h.settingRepo.Set("pull_list_hour", strconv.Itoa(*req.Hour)); err != nil {
			writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
			return
		}
	}

	// Return current state
	enabled, _ := h.settingRepo.Get("pull_list_enabled")
	day, _ := h.settingRepo.Get("pull_list_day")
	hour, _ := h.settingRepo.Get("pull_list_hour")
	lastRun, _ := h.settingRepo.Get("pull_list_last_run")

	dayInt := 3
	if d, err := strconv.Atoi(day); err == nil {
		dayInt = d
	}
	hourInt := 6
	if h2, err := strconv.Atoi(hour); err == nil {
		hourInt = h2
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":              "updated",
		"pull_list_enabled":   enabled == "true",
		"pull_list_day":       dayInt,
		"pull_list_hour":      hourInt,
		"pull_list_last_run":  lastRun,
	})
}

// slackSettingKeys is the whitelist of allowed Slack setting keys.
var slackSettingKeys = map[string]bool{
	"slack_enabled":                              true,
	"slack_webhook_url":                          true,
	"slack_notify_scan_complete":                 true,
	"slack_notify_metadata_refresh_complete":     true,
	"slack_notify_pull_list_search_complete":     true,
	"slack_notify_download_grabbed":              true,
	"slack_notify_download_complete":             true,
	"slack_notify_download_failed":               true,
}

// GetSlackSettings returns Slack notification configuration.
// GET /api/v1/settings/slack
func (h *SettingsHandler) GetSlackSettings(w http.ResponseWriter, r *http.Request) {
	enabled, _ := h.settingRepo.Get("slack_enabled")
	webhookURL, _ := h.settingRepo.Get("slack_webhook_url")

	// Mask the webhook URL
	maskedURL := ""
	if webhookURL != "" {
		if len(webhookURL) > 12 {
			maskedURL = webhookURL[:12] + strings.Repeat("*", len(webhookURL)-12)
		} else {
			maskedURL = strings.Repeat("*", len(webhookURL))
		}
	}

	toggleKeys := []string{
		"slack_notify_scan_complete",
		"slack_notify_metadata_refresh_complete",
		"slack_notify_pull_list_search_complete",
		"slack_notify_download_grabbed",
		"slack_notify_download_complete",
		"slack_notify_download_failed",
	}
	toggles := make(map[string]bool, len(toggleKeys))
	for _, key := range toggleKeys {
		val, _ := h.settingRepo.Get(key)
		// Default to true (enabled) unless explicitly "false"
		toggles[key] = val != "false"
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"slack_enabled":     enabled == "true",
		"slack_webhook_url": maskedURL,
		"slack_webhook_set": webhookURL != "",
		"toggles":           toggles,
	})
}

// UpdateSlackSettings saves Slack notification settings.
// PUT /api/v1/settings/slack
func (h *SettingsHandler) UpdateSlackSettings(w http.ResponseWriter, r *http.Request) {
	var req map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	for key, val := range req {
		if !slackSettingKeys[key] {
			writeError(w, http.StatusBadRequest, "INVALID_KEY", fmt.Sprintf("unknown setting key: %s", key))
			return
		}
		strVal := fmt.Sprintf("%v", val)
		if err := h.settingRepo.Set(key, strVal); err != nil {
			writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "updated"})
}

// TestSlackWebhook sends a test message to the configured Slack webhook.
// POST /api/v1/settings/slack/test
func (h *SettingsHandler) TestSlackWebhook(w http.ResponseWriter, r *http.Request) {
	webhookURL, err := h.settingRepo.Get("slack_webhook_url")
	if err != nil || webhookURL == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"message": "No Slack webhook URL configured",
		})
		return
	}

	client := slack.NewClient(webhookURL)
	if err := client.TestWebhook(); err != nil {
		slog.Warn("slack test webhook failed", "error", err)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Test message sent successfully",
	})
}

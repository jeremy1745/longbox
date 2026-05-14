package handler

import (
	"context"
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

// prowlarrConfigurator is the narrow interface the settings handler needs from
// *prowlarr.Client. Defined here to avoid a circular import with the prowlarr
// package and to allow easy testing.
type prowlarrConfigurator interface {
	SetConfig(baseURL, apiKey, category string)
	Configured() bool
	TestConnection(ctx context.Context) error
}

type SettingsHandler struct {
	metaSvc        *service.MetadataService
	librarySvc     *service.LibraryService
	watcher        *scanner.Watcher
	scheduler      *scheduler.Scheduler
	settingRepo    *repository.SettingRepo
	prowlarrClient prowlarrConfigurator
}

func NewSettingsHandler(
	metaSvc *service.MetadataService,
	librarySvc *service.LibraryService,
	watcher *scanner.Watcher,
	sched *scheduler.Scheduler,
	settingRepo *repository.SettingRepo,
	prowlarrClient prowlarrConfigurator,
) *SettingsHandler {
	return &SettingsHandler{
		metaSvc:        metaSvc,
		librarySvc:     librarySvc,
		watcher:        watcher,
		scheduler:      sched,
		settingRepo:    settingRepo,
		prowlarrClient: prowlarrClient,
	}
}

// GetSettings returns current application settings.
// GET /api/v1/settings
func (h *SettingsHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	settings := h.metaSvc.GetSettings()

	// Prowlarr status — surface URL and configured bool; never expose the key.
	prowlarrURL, _ := h.settingRepo.Get("prowlarr_url")
	settings["prowlarr_url"] = prowlarrURL
	settings["prowlarr_configured"] = h.prowlarrClient != nil && h.prowlarrClient.Configured()

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

// UpdateMetronCredentials saves Metron username + API token.
// PUT /api/v1/settings/metron
// Body: {"username": "...", "api_token": "..."}
func (h *SettingsHandler) UpdateMetronCredentials(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		APIToken string `json:"api_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if req.Username == "" || req.APIToken == "" {
		writeError(w, http.StatusBadRequest, "MISSING_FIELDS", "username and api_token are both required")
		return
	}
	if err := h.metaSvc.SetMetronCredentials(req.Username, req.APIToken); err != nil {
		writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":               "updated",
		"metron_username":      h.metaSvc.MetronUsername(),
		"metron_token_masked":  h.metaSvc.MetronTokenMasked(),
		"metron_token_set":     h.metaSvc.MetronTokenSet(),
	})
}

// TestMetron makes a tiny live request to verify Metron credentials.
// POST /api/v1/settings/metron/test
func (h *SettingsHandler) TestMetron(w http.ResponseWriter, r *http.Request) {
	if !h.metaSvc.HasMetronCredentials() {
		writeJSON(w, http.StatusOK, map[string]any{
			"valid":   false,
			"message": "No Metron credentials configured",
		})
		return
	}
	if err := h.metaSvc.TestMetron(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"valid":   false,
			"message": err.Error(),
		})
		return
	}
	q := h.metaSvc.MetronQuota()
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":               true,
		"message":             "Metron credentials are valid",
		"burst_limit":         q.BurstLimit,
		"burst_remaining":     q.BurstRemaining,
		"sustained_limit":     q.SustainedLimit,
		"sustained_remaining": q.SustainedRemaining,
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

// UpdateAutoSearch saves the auto-search-on-add setting.
// PUT /api/v1/settings/auto-search
// Body: {"enabled": true}
func (h *SettingsHandler) UpdateAutoSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	val := "false"
	if req.Enabled {
		val = "true"
	}
	if err := h.settingRepo.Set("auto_search_on_add", val); err != nil {
		writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":             "updated",
		"auto_search_on_add": req.Enabled,
	})
}

// UpdateAutoScan saves the automated library scan settings.
// PUT /api/v1/settings/auto-scan
// Body: {"enabled": true, "interval": 60}
func (h *SettingsHandler) UpdateAutoScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled  *bool `json:"enabled"`
		Interval *int  `json:"interval"`
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
		if err := h.settingRepo.Set("auto_scan_enabled", val); err != nil {
			writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
			return
		}
	}

	if req.Interval != nil {
		if *req.Interval < 5 || *req.Interval > 1440 {
			writeError(w, http.StatusBadRequest, "INVALID_INTERVAL", "interval must be 5-1440 minutes")
			return
		}
		if err := h.settingRepo.Set("auto_scan_interval", strconv.Itoa(*req.Interval)); err != nil {
			writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
			return
		}
	}

	enabled, _ := h.settingRepo.Get("auto_scan_enabled")
	intervalStr, _ := h.settingRepo.Get("auto_scan_interval")
	lastRun, _ := h.settingRepo.Get("auto_scan_last_run")

	interval := 60
	if i, err := strconv.Atoi(intervalStr); err == nil {
		interval = i
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":              "updated",
		"auto_scan_enabled":   enabled == "true",
		"auto_scan_interval":  interval,
		"auto_scan_last_run":  lastRun,
	})
}

// UpdateScanReconcile saves the scan-time reconciliation settings:
// auto-queue backlog runs when a CV refresh uncovers new gaps, and the
// per-series CV-refresh TTL in hours.
// PUT /api/v1/settings/scan-reconcile
// Body: {"auto_queue_backlog": true, "cv_refresh_ttl_hours": 24}
func (h *SettingsHandler) UpdateScanReconcile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AutoQueueBacklog  *bool `json:"auto_queue_backlog"`
		CVRefreshTTLHours *int  `json:"cv_refresh_ttl_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if req.AutoQueueBacklog != nil {
		val := "false"
		if *req.AutoQueueBacklog {
			val = "true"
		}
		if err := h.settingRepo.Set("scan_auto_queue_backlog", val); err != nil {
			writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
			return
		}
	}

	if req.CVRefreshTTLHours != nil {
		if *req.CVRefreshTTLHours < 1 || *req.CVRefreshTTLHours > 24*30 {
			writeError(w, http.StatusBadRequest, "INVALID_TTL", "cv_refresh_ttl_hours must be 1-720")
			return
		}
		if err := h.settingRepo.Set("scan_cv_refresh_ttl_hours", strconv.Itoa(*req.CVRefreshTTLHours)); err != nil {
			writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
			return
		}
	}

	autoQueue, _ := h.settingRepo.Get("scan_auto_queue_backlog")
	ttlStr, _ := h.settingRepo.Get("scan_cv_refresh_ttl_hours")
	ttl := 24
	if v, err := strconv.Atoi(ttlStr); err == nil && v > 0 {
		ttl = v
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":                    "updated",
		"scan_auto_queue_backlog":   autoQueue == "true",
		"scan_cv_refresh_ttl_hours": ttl,
	})
}

// UpdateMissingSearch saves missing issue search settings.
// PUT /api/v1/settings/missing-search
// Body: {"enabled": true, "interval": 10}
func (h *SettingsHandler) UpdateMissingSearch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled  *bool `json:"enabled"`
		Interval *int  `json:"interval"`
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
		if err := h.settingRepo.Set("missing_search_enabled", val); err != nil {
			writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
			return
		}
	}

	if req.Interval != nil {
		if *req.Interval < 1 || *req.Interval > 1440 {
			writeError(w, http.StatusBadRequest, "INVALID_INTERVAL", "interval must be 1-1440 minutes")
			return
		}
		if err := h.settingRepo.Set("missing_search_interval", strconv.Itoa(*req.Interval)); err != nil {
			writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
			return
		}
	}

	// Return current state
	enabled, _ := h.settingRepo.Get("missing_search_enabled")
	intervalStr, _ := h.settingRepo.Get("missing_search_interval")
	lastRun, _ := h.settingRepo.Get("missing_search_last_run")

	interval := 10
	if i, err := strconv.Atoi(intervalStr); err == nil {
		interval = i
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":                   "updated",
		"missing_search_enabled":   enabled == "true",
		"missing_search_interval":  interval,
		"missing_search_last_run":  lastRun,
	})
}

// UpdatePostProcessScript saves the post-processing script path.
// PUT /api/v1/settings/post-process-script
func (h *SettingsHandler) UpdatePostProcessScript(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ScriptPath string `json:"script_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	path := strings.TrimSpace(req.ScriptPath)

	// Allow clearing the script
	if path != "" {
		// Validate the script exists and is executable
		info, err := os.Stat(path)
		if err != nil {
			writeError(w, http.StatusBadRequest, "SCRIPT_NOT_FOUND",
				fmt.Sprintf("script not found: %s", path))
			return
		}
		if info.IsDir() {
			writeError(w, http.StatusBadRequest, "NOT_A_FILE", "path is a directory, not a script")
			return
		}
	}

	if err := h.settingRepo.Set("post_process_script", path); err != nil {
		writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":              "updated",
		"post_process_script": path,
	})
}

// slackSettingKeys is the whitelist of allowed Slack setting keys.
var slackSettingKeys = map[string]bool{
	"slack_enabled":                              true,
	"slack_bot_token":                            true,
	"slack_channel":                              true,
	"slack_notify_scan_complete":                 true,
	"slack_notify_metadata_refresh_complete":     true,
	"slack_notify_pull_list_search_complete":     true,
	"slack_notify_download_grabbed":              true,
	"slack_notify_download_complete":             true,
	"slack_notify_download_failed":               true,
	"slack_notify_missing_search_complete":       true,
}

// GetSlackSettings returns Slack notification configuration.
// GET /api/v1/settings/slack
func (h *SettingsHandler) GetSlackSettings(w http.ResponseWriter, r *http.Request) {
	enabled, _ := h.settingRepo.Get("slack_enabled")
	token, _ := h.settingRepo.Get("slack_bot_token")
	channel, _ := h.settingRepo.Get("slack_channel")

	// Mask the bot token — show first 8 chars
	maskedToken := ""
	if token != "" {
		if len(token) > 8 {
			maskedToken = token[:8] + strings.Repeat("*", len(token)-8)
		} else {
			maskedToken = strings.Repeat("*", len(token))
		}
	}

	toggleKeys := []string{
		"slack_notify_scan_complete",
		"slack_notify_metadata_refresh_complete",
		"slack_notify_pull_list_search_complete",
		"slack_notify_download_grabbed",
		"slack_notify_download_complete",
		"slack_notify_download_failed",
		"slack_notify_missing_search_complete",
	}
	toggles := make(map[string]bool, len(toggleKeys))
	for _, key := range toggleKeys {
		val, _ := h.settingRepo.Get(key)
		// Default to true (enabled) unless explicitly "false"
		toggles[key] = val != "false"
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"slack_enabled":   enabled == "true",
		"slack_bot_token": maskedToken,
		"slack_token_set": token != "",
		"slack_channel":   channel,
		"toggles":         toggles,
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

// UpdateProwlarrSettings saves Prowlarr connection settings and hot-reloads the live client.
// PUT /api/v1/settings/prowlarr
// Body: {"url": string, "api_key": string, "category": string}
func (h *SettingsHandler) UpdateProwlarrSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL      string `json:"url"`
		APIKey   string `json:"api_key"`
		Category string `json:"category"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if err := h.settingRepo.Set("prowlarr_url", req.URL); err != nil {
		writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
		return
	}
	if err := h.settingRepo.Set("prowlarr_api_key", req.APIKey); err != nil {
		writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
		return
	}
	category := req.Category
	if category == "" {
		category = "7030"
	}
	if err := h.settingRepo.Set("prowlarr_category", category); err != nil {
		writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
		return
	}

	// Hot-reload the live Prowlarr client so the new settings take effect immediately.
	if h.prowlarrClient != nil {
		h.prowlarrClient.SetConfig(req.URL, req.APIKey, category)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":              "updated",
		"prowlarr_url":        req.URL,
		"prowlarr_configured": h.prowlarrClient != nil && h.prowlarrClient.Configured(),
	})
}

// TestProwlarr checks whether the configured Prowlarr instance is reachable.
// POST /api/v1/settings/prowlarr/test
func (h *SettingsHandler) TestProwlarr(w http.ResponseWriter, r *http.Request) {
	if h.prowlarrClient == nil || !h.prowlarrClient.Configured() {
		writeJSON(w, http.StatusOK, map[string]any{
			"valid":   false,
			"message": "No Prowlarr URL/API key configured",
		})
		return
	}
	if err := h.prowlarrClient.TestConnection(r.Context()); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"valid":   false,
			"message": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valid":   true,
		"message": "Prowlarr connection OK",
	})
}

// TestSlack sends a test message to the configured Slack channel.
// POST /api/v1/settings/slack/test
func (h *SettingsHandler) TestSlack(w http.ResponseWriter, r *http.Request) {
	token, err := h.settingRepo.Get("slack_bot_token")
	if err != nil || token == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"message": "No Slack bot token configured",
		})
		return
	}

	channel, err := h.settingRepo.Get("slack_channel")
	if err != nil || channel == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"message": "No Slack channel configured",
		})
		return
	}

	client := slack.NewClient(token, channel)
	if err := client.Test(); err != nil {
		slog.Warn("slack test failed", "error", err)
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

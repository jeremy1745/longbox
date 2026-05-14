package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/service"
)

type BackupHandler struct {
	backupSvc   *service.BackupService
	settingRepo *repository.SettingRepo
}

func NewBackupHandler(backupSvc *service.BackupService, settingRepo *repository.SettingRepo) *BackupHandler {
	return &BackupHandler{backupSvc: backupSvc, settingRepo: settingRepo}
}

// Create creates a new database backup.
// POST /api/v1/admin/backup
func (h *BackupHandler) Create(w http.ResponseWriter, r *http.Request) {
	backup, err := h.backupSvc.CreateBackup()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "BACKUP_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, backup)
}

// List returns all available backups.
// GET /api/v1/admin/backups
func (h *BackupHandler) List(w http.ResponseWriter, r *http.Request) {
	backups, err := h.backupSvc.ListBackups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}
	if backups == nil {
		backups = []service.BackupInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"backups": backups,
		"total":   len(backups),
	})
}

// Delete removes a backup file.
// DELETE /api/v1/admin/backups/{name}
func (h *BackupHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "MISSING_NAME", "backup name is required")
		return
	}

	if err := h.backupSvc.DeleteBackup(name); err != nil {
		writeError(w, http.StatusInternalServerError, "DELETE_FAILED", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// Download serves a backup file for download.
// GET /api/v1/admin/backups/{name}/download
func (h *BackupHandler) Download(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" {
		writeError(w, http.StatusBadRequest, "MISSING_NAME", "backup name is required")
		return
	}

	path, err := h.backupSvc.BackupFilePath(name)
	if err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	w.Header().Set("Content-Disposition", "attachment; filename="+name)
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, path)
}

// UpdateBackupSettings saves backup configuration.
// PUT /api/v1/settings/backup
func (h *BackupHandler) UpdateBackupSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BackupOnStart   *bool `json:"backup_on_start"`
		BackupRetention *int  `json:"backup_retention"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if req.BackupOnStart != nil {
		val := "false"
		if *req.BackupOnStart {
			val = "true"
		}
		if err := h.settingRepo.Set("backup_on_start", val); err != nil {
			writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
			return
		}
	}

	if req.BackupRetention != nil {
		if *req.BackupRetention < 1 || *req.BackupRetention > 100 {
			writeError(w, http.StatusBadRequest, "INVALID_RETENTION", "retention must be 1-100")
			return
		}
		if err := h.settingRepo.Set("backup_retention", strconv.Itoa(*req.BackupRetention)); err != nil {
			writeError(w, http.StatusInternalServerError, "SAVE_FAILED", err.Error())
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

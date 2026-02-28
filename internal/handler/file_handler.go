package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/repository"
)

type FileHandler struct {
	fileRepo *repository.FileRepo
}

func NewFileHandler(fileRepo *repository.FileRepo) *FileHandler {
	return &FileHandler{fileRepo: fileRepo}
}

func (h *FileHandler) List(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	} else if perPage > 500 {
		perPage = 500
	}

	search := strings.TrimSpace(r.URL.Query().Get("search"))

	var files []interface{}
	var total int
	var err error

	if search != "" {
		f, t, e := h.fileRepo.Search(search, page, perPage)
		files = make([]interface{}, len(f))
		for i := range f {
			files[i] = f[i]
		}
		total, err = t, e
	} else {
		f, t, e := h.fileRepo.List(page, perPage)
		files = make([]interface{}, len(f))
		for i := range f {
			files[i] = f[i]
		}
		total, err = t, e
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"files":    files,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

func (h *FileHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid file ID")
		return
	}

	file, err := h.fileRepo.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FETCH_FAILED", err.Error())
		return
	}
	if file == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "file not found")
		return
	}

	writeJSON(w, http.StatusOK, file)
}

func (h *FileHandler) ListBySeries(w http.ResponseWriter, r *http.Request) {
	seriesID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid series ID")
		return
	}

	files, err := h.fileRepo.ListBySeries(seriesID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"files": files,
	})
}

type renameRequest struct {
	FileName string `json:"file_name"`
}

func (h *FileHandler) Rename(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid file ID")
		return
	}

	var req renameRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	newName := strings.TrimSpace(req.FileName)
	if newName == "" {
		writeError(w, http.StatusBadRequest, "INVALID_NAME", "file_name is required")
		return
	}

	// Reject path separators
	if strings.ContainsAny(newName, "/\\") {
		writeError(w, http.StatusBadRequest, "INVALID_NAME", "file name must not contain path separators")
		return
	}

	// Look up existing file
	file, err := h.fileRepo.GetByID(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "FETCH_FAILED", err.Error())
		return
	}
	if file == nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "file not found")
		return
	}

	// Validate extension matches
	oldExt := strings.ToLower(filepath.Ext(file.FileName))
	newExt := strings.ToLower(filepath.Ext(newName))
	if newExt != oldExt {
		writeError(w, http.StatusBadRequest, "INVALID_EXTENSION",
			"new file name must preserve the original extension ("+oldExt+")")
		return
	}

	// Verify old file exists on disk
	if _, err := os.Stat(file.FilePath); os.IsNotExist(err) {
		writeError(w, http.StatusConflict, "FILE_MISSING", "original file not found on disk")
		return
	}

	// Build new path (same directory, new name)
	dir := filepath.Dir(file.FilePath)
	newPath := filepath.Join(dir, newName)

	// Check target doesn't already exist (unless it's the same file)
	if newPath != file.FilePath {
		if _, err := os.Stat(newPath); err == nil {
			writeError(w, http.StatusConflict, "TARGET_EXISTS", "a file with that name already exists")
			return
		}
	}

	// Rename on disk
	if err := os.Rename(file.FilePath, newPath); err != nil {
		writeError(w, http.StatusInternalServerError, "RENAME_FAILED", "failed to rename file on disk: "+err.Error())
		return
	}

	// Update DB
	if err := h.fileRepo.UpdatePath(id, newPath, newName); err != nil {
		slog.Error("DB update failed after file rename, rolling back",
			"file_id", id, "new_path", newPath, "error", err)
		// Rollback disk rename
		if rbErr := os.Rename(newPath, file.FilePath); rbErr != nil {
			slog.Error("rollback failed! File is at new location but DB has old path",
				"file_id", id, "actual_path", newPath, "db_path", file.FilePath)
		}
		writeError(w, http.StatusInternalServerError, "DB_UPDATE_FAILED", "failed to update database after rename")
		return
	}

	// Fetch updated file to return
	updated, err := h.fileRepo.GetByID(id)
	if err != nil || updated == nil {
		// Rename succeeded, just return what we know
		file.FilePath = newPath
		file.FileName = newName
		writeJSON(w, http.StatusOK, file)
		return
	}

	writeJSON(w, http.StatusOK, updated)
}

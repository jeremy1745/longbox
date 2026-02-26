package handler

import (
	"net/http"
	"strconv"

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
	}

	files, total, err := h.fileRepo.List(page, perPage)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"files": files,
		"total": total,
		"page":  page,
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

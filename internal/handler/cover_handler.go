package handler

import (
	"net/http"
	"os"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/service"
)

type CoverHandler struct {
	coverSvc *service.CoverService
	fileRepo *repository.FileRepo
}

func NewCoverHandler(coverSvc *service.CoverService, fileRepo *repository.FileRepo) *CoverHandler {
	return &CoverHandler{coverSvc: coverSvc, fileRepo: fileRepo}
}

func (h *CoverHandler) ServeFileCover(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid file ID")
		return
	}

	coverPath := h.coverSvc.CoverPath(id)
	if _, err := os.Stat(coverPath); os.IsNotExist(err) {
		// Try to extract the cover on the fly
		file, err := h.fileRepo.GetByID(id)
		if err != nil || file == nil {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "file not found")
			return
		}
		coverPath, err = h.coverSvc.ExtractCover(id, file.FilePath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "COVER_FAILED", "failed to extract cover")
			return
		}
	}

	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeFile(w, r, coverPath)
}

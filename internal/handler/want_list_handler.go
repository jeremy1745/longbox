package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/service"
)

type WantListHandler struct {
	wantListRepo *repository.WantListRepo
	searchSvc    *service.SearchService
	settingRepo  *repository.SettingRepo
}

func NewWantListHandler(wantListRepo *repository.WantListRepo, searchSvc *service.SearchService, settingRepo *repository.SettingRepo) *WantListHandler {
	return &WantListHandler{
		wantListRepo: wantListRepo,
		searchSvc:    searchSvc,
		settingRepo:  settingRepo,
	}
}

func (h *WantListHandler) List(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	sortBy := r.URL.Query().Get("sort")
	order := r.URL.Query().Get("order")

	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 100
	} else if perPage > 500 {
		perPage = 500
	}

	items, total, err := h.wantListRepo.List(page, perPage, sortBy, order)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LIST_FAILED", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"items":    items,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

func (h *WantListHandler) Add(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IssueID  int64  `json:"issue_id"`
		Priority int    `json:"priority"`
		Notes    string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}
	if body.IssueID == 0 {
		writeError(w, http.StatusBadRequest, "MISSING_ISSUE_ID", "issue_id is required")
		return
	}

	item, err := h.wantListRepo.Create(body.IssueID, body.Priority, body.Notes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ADD_FAILED", err.Error())
		return
	}

	triggerAutoSearch(h.searchSvc, h.settingRepo, body.IssueID, fmt.Sprintf("want-list issue %d", body.IssueID))

	writeJSON(w, http.StatusCreated, item)
}

func (h *WantListHandler) Remove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid want list item ID")
		return
	}

	if err := h.wantListRepo.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func (h *WantListHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ID", "invalid want list item ID")
		return
	}

	var body struct {
		Priority int    `json:"priority"`
		Notes    string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body")
		return
	}

	if err := h.wantListRepo.Update(id, body.Priority, body.Notes); err != nil {
		writeError(w, http.StatusNotFound, "NOT_FOUND", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{"updated": true})
}

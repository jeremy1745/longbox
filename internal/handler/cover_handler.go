package handler

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/service"
)

type CoverHandler struct {
	coverSvc *service.CoverService
	fileRepo *repository.FileRepo
	http     *http.Client
}

func NewCoverHandler(coverSvc *service.CoverService, fileRepo *repository.FileRepo) *CoverHandler {
	return &CoverHandler{
		coverSvc: coverSvc,
		fileRepo: fileRepo,
		http:     &http.Client{Timeout: 20 * time.Second},
	}
}

// providerImageHosts is the allowlist for ProxyURL. Anything else gets rejected
// so the proxy can't be turned into an open-relay against arbitrary sites.
var providerImageHosts = map[string]bool{
	"comicvine.gamespot.com":   true,
	"comicvine1.cbsistatic.com": true,
	"comicvine2.cbsistatic.com": true,
	"comicvine3.cbsistatic.com": true,
	"comicvine4.cbsistatic.com": true,
	"static.metron.cloud":      true,
	"metron.cloud":             true,
	"www.metron.cloud":         true,
}

// ProxyURL fetches a provider image URL server-side and streams it to the
// client. Bypasses browser referrer / hot-link blocking that breaks direct
// <img src=https://...> for ComicVine and Metron covers.
//
// GET /api/v1/covers/proxy?u={url-encoded}
func (h *CoverHandler) ProxyURL(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("u")
	if raw == "" {
		writeError(w, http.StatusBadRequest, "MISSING_URL", "missing u parameter")
		return
	}
	target, err := url.Parse(raw)
	if err != nil || target.Scheme != "https" {
		writeError(w, http.StatusBadRequest, "INVALID_URL", "url must be absolute https")
		return
	}
	// Use Hostname() (not Host) so an explicit port like "host:443" still
	// matches the allowlist entries which are bare hostnames.
	host := strings.ToLower(target.Hostname())
	if !providerImageHosts[host] {
		writeError(w, http.StatusForbidden, "HOST_NOT_ALLOWED",
			"host is not in the provider allowlist: "+host)
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "PROXY_FAILED", err.Error())
		return
	}
	req.Header.Set("User-Agent", "longbox/1.0")
	resp, err := h.http.Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "UPSTREAM_FAILED", err.Error())
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, "UPSTREAM_STATUS",
			"upstream returned "+strconv.Itoa(resp.StatusCode))
		return
	}

	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	} else {
		w.Header().Set("Content-Type", "image/jpeg")
	}
	w.Header().Set("Cache-Control", "public, max-age=604800") // 7 days
	io.Copy(w, io.LimitReader(resp.Body, 16<<20))
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

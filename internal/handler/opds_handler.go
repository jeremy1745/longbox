package handler

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/service"
)

// OPDS Atom XML types

type OPDSFeed struct {
	XMLName xml.Name    `xml:"feed"`
	XMLNS   string      `xml:"xmlns,attr"`
	OPDS    string      `xml:"xmlns:opds,attr,omitempty"`
	ID      string      `xml:"id"`
	Title   string      `xml:"title"`
	Updated string      `xml:"updated"`
	Author  *OPDSAuthor `xml:"author,omitempty"`
	Links   []OPDSLink  `xml:"link"`
	Entries []OPDSEntry `xml:"entry"`
}

type OPDSEntry struct {
	Title   string     `xml:"title"`
	ID      string     `xml:"id"`
	Updated string     `xml:"updated"`
	Content *OPDSText  `xml:"content,omitempty"`
	Links   []OPDSLink `xml:"link"`
}

type OPDSLink struct {
	Rel  string `xml:"rel,attr,omitempty"`
	Href string `xml:"href,attr"`
	Type string `xml:"type,attr,omitempty"`
}

type OPDSText struct {
	Type string `xml:"type,attr"`
	Body string `xml:",chardata"`
}

type OPDSAuthor struct {
	Name string `xml:"name"`
}

type OPDSHandler struct {
	fileRepo   *repository.FileRepo
	seriesRepo *repository.SeriesRepo
	coverSvc   *service.CoverService
}

func NewOPDSHandler(
	fileRepo *repository.FileRepo,
	seriesRepo *repository.SeriesRepo,
	coverSvc *service.CoverService,
) *OPDSHandler {
	return &OPDSHandler{
		fileRepo:   fileRepo,
		seriesRepo: seriesRepo,
		coverSvc:   coverSvc,
	}
}

func (h *OPDSHandler) writeOPDS(w http.ResponseWriter, feed *OPDSFeed) {
	w.Header().Set("Content-Type", "application/atom+xml;charset=UTF-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	enc.Encode(feed)
}

// Root serves the OPDS root navigation catalog.
// GET /opds/
func (h *OPDSHandler) Root(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC().Format(time.RFC3339)
	feed := &OPDSFeed{
		XMLNS:   "http://www.w3.org/2005/Atom",
		OPDS:    "http://opds-spec.org/2010/catalog",
		ID:      "longbox:root",
		Title:   "LongBox",
		Updated: now,
		Author:  &OPDSAuthor{Name: "LongBox"},
		Links: []OPDSLink{
			{Rel: "self", Href: "/opds/", Type: "application/atom+xml;profile=opds-catalog;kind=navigation"},
			{Rel: "start", Href: "/opds/", Type: "application/atom+xml;profile=opds-catalog;kind=navigation"},
			{Rel: "search", Href: "/opds/search?q={searchTerms}", Type: "application/atom+xml"},
		},
		Entries: []OPDSEntry{
			{
				Title:   "All Series",
				ID:      "longbox:series",
				Updated: now,
				Content: &OPDSText{Type: "text", Body: "Browse comics by series"},
				Links: []OPDSLink{
					{Rel: "subsection", Href: "/opds/series", Type: "application/atom+xml;profile=opds-catalog;kind=acquisition"},
				},
			},
			{
				Title:   "Recently Added",
				ID:      "longbox:recent",
				Updated: now,
				Content: &OPDSText{Type: "text", Body: "Recently added comics"},
				Links: []OPDSLink{
					{Rel: "subsection", Href: "/opds/recent", Type: "application/atom+xml;profile=opds-catalog;kind=acquisition"},
				},
			},
		},
	}
	h.writeOPDS(w, feed)
}

// SeriesCatalog lists all series that have files.
// GET /opds/series
func (h *OPDSHandler) SeriesCatalog(w http.ResponseWriter, r *http.Request) {
	seriesList, _, err := h.seriesRepo.List(1, 10000, "title", "asc")
	if err != nil {
		http.Error(w, "failed to list series", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	entries := make([]OPDSEntry, 0, len(seriesList))
	for _, s := range seriesList {
		if s.FileCount == 0 {
			continue
		}
		title := s.Title
		if s.Year != nil {
			title = fmt.Sprintf("%s (%d)", s.Title, *s.Year)
		}
		entry := OPDSEntry{
			Title:   title,
			ID:      fmt.Sprintf("longbox:series:%d", s.ID),
			Updated: s.UpdatedAt.UTC().Format(time.RFC3339),
			Content: &OPDSText{Type: "text", Body: fmt.Sprintf("%d issues", s.FileCount)},
			Links: []OPDSLink{
				{Rel: "subsection", Href: fmt.Sprintf("/opds/series/%d", s.ID), Type: "application/atom+xml;profile=opds-catalog;kind=acquisition"},
			},
		}
		if s.CoverFileID != nil {
			entry.Links = append(entry.Links, OPDSLink{
				Rel:  "http://opds-spec.org/image/thumbnail",
				Href: fmt.Sprintf("/opds/cover/%d", *s.CoverFileID),
				Type: "image/jpeg",
			})
		}
		entries = append(entries, entry)
	}

	feed := &OPDSFeed{
		XMLNS:   "http://www.w3.org/2005/Atom",
		OPDS:    "http://opds-spec.org/2010/catalog",
		ID:      "longbox:series",
		Title:   "All Series",
		Updated: now,
		Links: []OPDSLink{
			{Rel: "self", Href: "/opds/series", Type: "application/atom+xml;profile=opds-catalog;kind=acquisition"},
			{Rel: "start", Href: "/opds/", Type: "application/atom+xml;profile=opds-catalog;kind=navigation"},
		},
		Entries: entries,
	}
	h.writeOPDS(w, feed)
}

// SeriesIssues lists all files for a series with acquisition links.
// GET /opds/series/{id}
func (h *OPDSHandler) SeriesIssues(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid series ID", http.StatusBadRequest)
		return
	}

	series, err := h.seriesRepo.GetByID(id)
	if err != nil || series == nil {
		http.Error(w, "series not found", http.StatusNotFound)
		return
	}

	files, err := h.fileRepo.ListBySeries(id)
	if err != nil {
		http.Error(w, "failed to list files", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	entries := make([]OPDSEntry, 0, len(files))
	for _, f := range files {
		title := f.FileName
		if f.ParsedNumber != "" {
			title = fmt.Sprintf("#%s", f.ParsedNumber)
		}

		entry := OPDSEntry{
			Title:   title,
			ID:      fmt.Sprintf("longbox:file:%d", f.ID),
			Updated: f.UpdatedAt.UTC().Format(time.RFC3339),
			Links: []OPDSLink{
				{Rel: "http://opds-spec.org/acquisition", Href: fmt.Sprintf("/opds/file/%d", f.ID), Type: comicMIME(f.FileFormat)},
			},
		}

		coverPath := h.coverSvc.CoverPath(f.ID)
		if _, err := os.Stat(coverPath); err == nil {
			entry.Links = append(entry.Links, OPDSLink{
				Rel:  "http://opds-spec.org/image/thumbnail",
				Href: fmt.Sprintf("/opds/cover/%d", f.ID),
				Type: "image/jpeg",
			})
		}

		entries = append(entries, entry)
	}

	seriesTitle := series.Title
	if series.Year != nil {
		seriesTitle = fmt.Sprintf("%s (%d)", series.Title, *series.Year)
	}

	feed := &OPDSFeed{
		XMLNS:   "http://www.w3.org/2005/Atom",
		OPDS:    "http://opds-spec.org/2010/catalog",
		ID:      fmt.Sprintf("longbox:series:%d", id),
		Title:   seriesTitle,
		Updated: now,
		Links: []OPDSLink{
			{Rel: "self", Href: fmt.Sprintf("/opds/series/%d", id), Type: "application/atom+xml;profile=opds-catalog;kind=acquisition"},
			{Rel: "up", Href: "/opds/series", Type: "application/atom+xml;profile=opds-catalog;kind=acquisition"},
			{Rel: "start", Href: "/opds/", Type: "application/atom+xml;profile=opds-catalog;kind=navigation"},
		},
		Entries: entries,
	}
	h.writeOPDS(w, feed)
}

// Recent shows the last 50 files added.
// GET /opds/recent
func (h *OPDSHandler) Recent(w http.ResponseWriter, r *http.Request) {
	files, _, err := h.fileRepo.List(1, 50)
	if err != nil {
		http.Error(w, "failed to list files", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	entries := make([]OPDSEntry, 0, len(files))
	for _, f := range files {
		title := f.FileName
		if f.ParsedSeries != "" && f.ParsedNumber != "" {
			title = fmt.Sprintf("%s #%s", f.ParsedSeries, f.ParsedNumber)
		}

		entry := OPDSEntry{
			Title:   title,
			ID:      fmt.Sprintf("longbox:file:%d", f.ID),
			Updated: f.CreatedAt.UTC().Format(time.RFC3339),
			Links: []OPDSLink{
				{Rel: "http://opds-spec.org/acquisition", Href: fmt.Sprintf("/opds/file/%d", f.ID), Type: comicMIME(f.FileFormat)},
			},
		}

		coverPath := h.coverSvc.CoverPath(f.ID)
		if _, err := os.Stat(coverPath); err == nil {
			entry.Links = append(entry.Links, OPDSLink{
				Rel:  "http://opds-spec.org/image/thumbnail",
				Href: fmt.Sprintf("/opds/cover/%d", f.ID),
				Type: "image/jpeg",
			})
		}

		entries = append(entries, entry)
	}

	feed := &OPDSFeed{
		XMLNS:   "http://www.w3.org/2005/Atom",
		OPDS:    "http://opds-spec.org/2010/catalog",
		ID:      "longbox:recent",
		Title:   "Recently Added",
		Updated: now,
		Links: []OPDSLink{
			{Rel: "self", Href: "/opds/recent", Type: "application/atom+xml;profile=opds-catalog;kind=acquisition"},
			{Rel: "start", Href: "/opds/", Type: "application/atom+xml;profile=opds-catalog;kind=navigation"},
		},
		Entries: entries,
	}
	h.writeOPDS(w, feed)
}

// Search searches series by name for OPDS clients.
// GET /opds/search?q={query}
func (h *OPDSHandler) Search(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		http.Error(w, "q parameter is required", http.StatusBadRequest)
		return
	}

	seriesList, _, err := h.seriesRepo.List(1, 100, "title", "asc")
	if err != nil {
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	queryLower := strings.ToLower(query)
	entries := make([]OPDSEntry, 0)
	for _, s := range seriesList {
		if s.FileCount == 0 {
			continue
		}
		if !strings.Contains(strings.ToLower(s.Title), queryLower) {
			continue
		}
		title := s.Title
		if s.Year != nil {
			title = fmt.Sprintf("%s (%d)", s.Title, *s.Year)
		}
		entry := OPDSEntry{
			Title:   title,
			ID:      fmt.Sprintf("longbox:series:%d", s.ID),
			Updated: s.UpdatedAt.UTC().Format(time.RFC3339),
			Content: &OPDSText{Type: "text", Body: fmt.Sprintf("%d issues", s.FileCount)},
			Links: []OPDSLink{
				{Rel: "subsection", Href: fmt.Sprintf("/opds/series/%d", s.ID), Type: "application/atom+xml;profile=opds-catalog;kind=acquisition"},
			},
		}
		entries = append(entries, entry)
	}

	feed := &OPDSFeed{
		XMLNS:   "http://www.w3.org/2005/Atom",
		OPDS:    "http://opds-spec.org/2010/catalog",
		ID:      "longbox:search",
		Title:   fmt.Sprintf("Search: %s", query),
		Updated: now,
		Links: []OPDSLink{
			{Rel: "self", Href: fmt.Sprintf("/opds/search?q=%s", query), Type: "application/atom+xml;profile=opds-catalog;kind=acquisition"},
			{Rel: "start", Href: "/opds/", Type: "application/atom+xml;profile=opds-catalog;kind=navigation"},
		},
		Entries: entries,
	}
	h.writeOPDS(w, feed)
}

// ServeFile serves a comic file for download.
// GET /opds/file/{id}
func (h *OPDSHandler) ServeFile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid file ID", http.StatusBadRequest)
		return
	}

	file, err := h.fileRepo.GetByID(id)
	if err != nil || file == nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", comicMIME(file.FileFormat))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file.FileName))
	http.ServeFile(w, r, file.FilePath)
}

// ServeCover serves a cover thumbnail.
// GET /opds/cover/{id}
func (h *OPDSHandler) ServeCover(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid file ID", http.StatusBadRequest)
		return
	}

	coverPath := h.coverSvc.CoverPath(id)
	if _, err := os.Stat(coverPath); os.IsNotExist(err) {
		http.Error(w, "cover not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeFile(w, r, coverPath)
}

// comicMIME returns the MIME type for a comic file format.
func comicMIME(format string) string {
	switch strings.ToLower(format) {
	case "cbz":
		return "application/vnd.comicbook+zip"
	case "cbr":
		return "application/vnd.comicbook-rar"
	case "pdf":
		return "application/pdf"
	default:
		return "application/octet-stream"
	}
}

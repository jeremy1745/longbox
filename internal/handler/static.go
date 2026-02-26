package handler

import (
	"io/fs"
	"net/http"
	"strings"
)

// SPAHandler serves the embedded Svelte frontend with SPA fallback routing.
type SPAHandler struct {
	fileServer http.Handler
	fsys       fs.FS
}

func NewSPAHandler(fsys fs.FS) *SPAHandler {
	return &SPAHandler{
		fileServer: http.FileServer(http.FS(fsys)),
		fsys:       fsys,
	}
}

func (h *SPAHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Don't handle API routes
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "NOT_FOUND", "endpoint not found")
		return
	}

	// Try to serve the static file
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}

	_, err := fs.Stat(h.fsys, path)
	if err == nil {
		h.fileServer.ServeHTTP(w, r)
		return
	}

	// SPA fallback: serve index.html for any path that doesn't match a file
	r.URL.Path = "/"
	h.fileServer.ServeHTTP(w, r)
}

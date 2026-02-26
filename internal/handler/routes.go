package handler

import (
	"io/fs"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scanner"
	"github.com/jeremy/longbox/internal/scheduler"
	"github.com/jeremy/longbox/internal/service"
)

func NewRouter(
	fileRepo *repository.FileRepo,
	seriesRepo *repository.SeriesRepo,
	issueRepo *repository.IssueRepo,
	jobRepo *repository.JobRepo,
	wantListRepo *repository.WantListRepo,
	librarySvc *service.LibraryService,
	coverSvc *service.CoverService,
	metaSvc *service.MetadataService,
	organizeSvc *service.FileOrganizerService,
	readerSvc *service.ReaderService,
	sched *scheduler.Scheduler,
	eventBus *scheduler.EventBus,
	watcher *scanner.Watcher,
	frontendFS fs.FS,
) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(Recovery)
	r.Use(RequestID)
	r.Use(Logger)
	r.Use(CORS)

	// API routes
	libraryH := NewLibraryHandler(librarySvc, fileRepo, seriesRepo, sched)
	fileH := NewFileHandler(fileRepo)
	coverH := NewCoverHandler(coverSvc, fileRepo)
	seriesH := NewSeriesHandler(seriesRepo, issueRepo, wantListRepo)
	issueH := NewIssueHandler(issueRepo)
	metaH := NewMetadataHandler(metaSvc)
	settingsH := NewSettingsHandler(metaSvc, librarySvc, watcher, sched)
	jobH := NewJobHandler(jobRepo, sched, eventBus)
	wantListH := NewWantListHandler(wantListRepo)
	calendarH := NewCalendarHandler(issueRepo)
	organizeH := NewOrganizeHandler(organizeSvc, librarySvc)
	readerH := NewReaderHandler(readerSvc, fileRepo, issueRepo)

	r.Route("/api/v1", func(r chi.Router) {
		// Library
		r.Post("/library/scan", libraryH.Scan)
		r.Get("/library/stats", libraryH.Stats)

		// Series
		r.Get("/series", seriesH.List)
		r.Get("/series/{id}", seriesH.Get)
		r.Get("/series/{id}/issues", seriesH.GetIssues)
		r.Put("/series/{id}/track", seriesH.Track)
		r.Put("/series/{id}/untrack", seriesH.Untrack)

		// Issues
		r.Get("/issues/{id}", issueH.Get)
		r.Put("/issues/{id}/read-status", issueH.UpdateReadStatus)

		// Files
		r.Get("/files", fileH.List)
		r.Get("/files/{id}", fileH.Get)

		// Covers
		r.Get("/covers/file/{id}", coverH.ServeFileCover)

		// Metadata (ComicVine)
		r.Get("/metadata/search", metaH.SearchVolumes)
		r.Get("/metadata/volume/{cvid}", metaH.GetVolume)
		r.Post("/series/{id}/match", metaH.MatchSeries)
		r.Post("/series/{id}/refresh", metaH.RefreshSeries)

		// Settings
		r.Get("/settings", settingsH.GetSettings)
		r.Put("/settings/comicvine-api-key", settingsH.UpdateAPIKey)
		r.Post("/settings/comicvine-api-key/test", settingsH.TestAPIKey)
		r.Put("/settings/library-dir", settingsH.UpdateLibraryDir)

		// Want List
		r.Get("/want-list", wantListH.List)
		r.Post("/want-list", wantListH.Add)
		r.Put("/want-list/{id}", wantListH.Update)
		r.Delete("/want-list/{id}", wantListH.Remove)

		// Calendar
		r.Get("/calendar", calendarH.Upcoming)

		// File Organization
		r.Get("/library/organize/template", organizeH.GetTemplate)
		r.Put("/library/organize/template", organizeH.SetTemplate)
		r.Post("/library/organize/preview", organizeH.Preview)
		r.Post("/library/organize/execute", organizeH.Execute)
		r.Post("/library/organize/preview-template", organizeH.PreviewTemplate)

		// Reader
		r.Get("/reader/{id}/pages", readerH.ListPages)
		r.Get("/reader/{id}/pages/{page}", readerH.ServePage)
		r.Put("/reader/{id}/progress", readerH.UpdateProgress)

		// Jobs
		r.Get("/jobs", jobH.List)
		r.Get("/jobs/{id}", jobH.Get)
		r.Post("/jobs/{id}/cancel", jobH.Cancel)

		// SSE Events
		r.Get("/events", jobH.Events)
	})

	// Serve embedded frontend (SPA with fallback)
	spaHandler := NewSPAHandler(frontendFS)
	r.NotFound(spaHandler.ServeHTTP)

	return r
}

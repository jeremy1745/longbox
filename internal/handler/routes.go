package handler

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scanner"
	"github.com/jeremy/longbox/internal/scheduler"
	"github.com/jeremy/longbox/internal/service"
)

// ShutdownRequester is implemented by the server to allow API-triggered shutdown.
type ShutdownRequester interface {
	RequestShutdown()
}

func NewRouter(
	fileRepo *repository.FileRepo,
	seriesRepo *repository.SeriesRepo,
	issueRepo *repository.IssueRepo,
	jobRepo *repository.JobRepo,
	wantListRepo *repository.WantListRepo,
	indexerRepo *repository.IndexerRepo,
	dlClientRepo *repository.DownloadClientRepo,
	dlHistoryRepo *repository.DownloadHistoryRepo,
	librarySvc *service.LibraryService,
	coverSvc *service.CoverService,
	metaSvc *service.MetadataService,
	organizeSvc *service.FileOrganizerService,
	readerSvc *service.ReaderService,
	searchSvc *service.SearchService,
	metaWriterSvc *service.MetadataWriterService,
	mylarSvc *service.MylarMetadataService,
	sched *scheduler.Scheduler,
	eventBus *scheduler.EventBus,
	watcher *scanner.Watcher,
	settingRepo *repository.SettingRepo,
	authSvc *service.AuthService,
	shutdownReq ShutdownRequester,
	frontendFS fs.FS,
) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(Recovery)
	r.Use(RequestID)
	r.Use(Logger)
	r.Use(SecurityHeaders)
	r.Use(MaxBodySize)

	// Handlers
	libraryH := NewLibraryHandler(librarySvc, fileRepo, seriesRepo, sched)
	fileH := NewFileHandler(fileRepo)
	coverH := NewCoverHandler(coverSvc, fileRepo)
	seriesH := NewSeriesHandler(seriesRepo, issueRepo, wantListRepo)
	issueH := NewIssueHandler(issueRepo)
	metaH := NewMetadataHandler(metaSvc)
	settingsH := NewSettingsHandler(metaSvc, librarySvc, watcher, sched, settingRepo)
	jobH := NewJobHandler(jobRepo, sched, eventBus)
	wantListH := NewWantListHandler(wantListRepo, searchSvc, settingRepo)
	calendarH := NewCalendarHandler(issueRepo, seriesRepo, wantListRepo, metaSvc, sched, searchSvc, settingRepo)
	organizeH := NewOrganizeHandler(organizeSvc, librarySvc)
	readerH := NewReaderHandler(readerSvc, fileRepo, issueRepo)
	indexerH := NewIndexerHandler(indexerRepo)
	dlClientH := NewDownloadClientHandler(dlClientRepo)
	searchH := NewSearchHandler(searchSvc, dlHistoryRepo, sched)
	metaWriterH := NewMetadataWriterHandler(metaWriterSvc)
	mylarH := NewMylarMetadataHandler(seriesRepo, sched)
	authH := NewAuthHandler(authSvc)

	// Rate limiter: 5 attempts per minute for auth endpoints
	authLimiter := NewRateLimiter(5, 1*time.Minute)

	r.Route("/api/v1", func(r chi.Router) {
		// Public auth routes (no auth required)
		r.Get("/auth/status", authH.Status)
		r.Post("/auth/login", authLimiter.RateLimit(authH.Login))
		r.Post("/auth/register", authLimiter.RateLimit(authH.Register))

		// Protected routes (auth required when enabled)
		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware(authSvc))

			// Auth routes requiring authentication
			r.Post("/auth/logout", authH.Logout)
			r.Get("/auth/me", authH.Me)
			r.Put("/auth/users/{id}/password", authH.ChangePassword)

			// Admin-only routes
			r.Group(func(r chi.Router) {
				r.Use(AdminOnly)
				r.Get("/auth/users", authH.ListUsers)
				r.Post("/auth/users", authH.CreateUser)
				r.Delete("/auth/users/{id}", authH.DeleteUser)
				r.Post("/admin/shutdown", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]string{"message": "server is shutting down"})
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					go func() {
						time.Sleep(500 * time.Millisecond)
						shutdownReq.RequestShutdown()
					}()
				})
			})

			// Library
			r.Post("/library/scan", libraryH.Scan)
			r.Get("/library/stats", libraryH.Stats)
			r.Post("/library/write-mylar-metadata", mylarH.WriteAll)

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
			r.Put("/files/{id}/rename", fileH.Rename)

			// Series files
			r.Get("/series/{id}/files", fileH.ListBySeries)

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
			r.Put("/settings/pull-list-schedule", settingsH.UpdatePullListSchedule)
			r.Put("/settings/auto-search", settingsH.UpdateAutoSearch)
			r.Put("/settings/missing-search", settingsH.UpdateMissingSearch)
			r.Put("/settings/auto-scan", settingsH.UpdateAutoScan)
			r.Get("/settings/slack", settingsH.GetSlackSettings)
			r.Put("/settings/slack", settingsH.UpdateSlackSettings)
			r.Post("/settings/slack/test", settingsH.TestSlack)

			// Want List
			r.Get("/want-list", wantListH.List)
			r.Post("/want-list", wantListH.Add)
			r.Put("/want-list/{id}", wantListH.Update)
			r.Delete("/want-list/{id}", wantListH.Remove)

			// Calendar / Pull List
			r.Get("/calendar", calendarH.Upcoming)
			r.Get("/calendar/releases", calendarH.Releases)
			r.Post("/calendar/refresh", calendarH.RefreshTracked)
			r.Post("/calendar/track", calendarH.TrackSeries)
			r.Post("/calendar/want", calendarH.WantIssue)

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

			// Indexers
			r.Get("/indexers", indexerH.List)
			r.Post("/indexers", indexerH.Create)
			r.Put("/indexers/{id}", indexerH.Update)
			r.Delete("/indexers/{id}", indexerH.Delete)
			r.Post("/indexers/{id}/test", indexerH.Test)

			// Download Clients
			r.Get("/download-clients", dlClientH.List)
			r.Post("/download-clients", dlClientH.Create)
			r.Put("/download-clients/{id}", dlClientH.Update)
			r.Delete("/download-clients/{id}", dlClientH.Delete)
			r.Post("/download-clients/{id}/test", dlClientH.Test)

			// Metadata Writing (ComicInfo.xml)
			r.Post("/files/{id}/write-metadata", metaWriterH.WriteToFile)
			r.Post("/files/write-metadata", metaWriterH.WriteToFiles)
			r.Post("/issues/{id}/write-metadata", metaWriterH.WriteToIssue)
			r.Post("/series/{id}/write-metadata", metaWriterH.WriteToSeries)

			// Search & Downloads
			r.Get("/search/issue/{id}", searchH.SearchIssue)
			r.Get("/search", searchH.SearchQuery)
			r.Post("/search/grab", searchH.Grab)
			r.Post("/search/pull-list", searchH.TriggerPullListSearch)
			r.Get("/downloads", searchH.DownloadHistory)
		})
	})

	// Serve embedded frontend (SPA with fallback)
	spaHandler := NewSPAHandler(frontendFS)
	r.NotFound(spaHandler.ServeHTTP)

	return r
}

package handler

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jeremy/longbox/internal/comicvine"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scanner"
	"github.com/jeremy/longbox/internal/scheduler"
	"github.com/jeremy/longbox/internal/service"
	"github.com/jeremy/longbox/internal/prowlarr"
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
	backlogRepo *repository.BacklogRepo,
	backlogSvc *service.BacklogService,
	indexerRepo *repository.IndexerRepo,
	dlClientRepo *repository.DownloadClientRepo,
	dlHistoryRepo *repository.DownloadHistoryRepo,
	blocklistRepo *repository.BlocklistRepo,
	storyArcRepo *repository.StoryArcRepo,
	cvClient *comicvine.Client,
	librarySvc *service.LibraryService,
	coverSvc *service.CoverService,
	metaSvc *service.MetadataService,
	organizeSvc *service.FileOrganizerService,
	readerSvc *service.ReaderService,
	searchSvc *service.SearchService,
	metaWriterSvc *service.MetadataWriterService,
	longboxMetadataSvc *service.LongboxMetadataService,
	folderImageSvc *service.FolderImageService,
	backupSvc *service.BackupService,
	sched *scheduler.Scheduler,
	eventBus *scheduler.EventBus,
	watcher *scanner.Watcher,
	settingRepo *repository.SettingRepo,
	authSvc *service.AuthService,
	shutdownReq ShutdownRequester,
	frontendFS fs.FS,
	prowlarrClient *prowlarr.Client,
	acqSvc *service.AcquisitionService,
) http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(Recovery)
	r.Use(RequestID)
	r.Use(Logger)
	r.Use(SecurityHeaders)
	r.Use(MaxBodySize)

	// Handlers
	libraryH := NewLibraryHandler(librarySvc, fileRepo, seriesRepo, issueRepo, sched, settingRepo, backlogRepo, indexerRepo, dlClientRepo, wantListRepo, organizeSvc)
	fileH := NewFileHandler(fileRepo, librarySvc, sched)
	coverH := NewCoverHandler(coverSvc, fileRepo)
	seriesH := NewSeriesHandler(seriesRepo, issueRepo, wantListRepo, librarySvc)
	issueH := NewIssueHandler(issueRepo, librarySvc, organizeSvc)
	metaH := NewMetadataHandler(metaSvc)
	settingsH := NewSettingsHandler(metaSvc, librarySvc, watcher, sched, settingRepo, prowlarrClient)
	acquisitionH := NewAcquisitionHandler(acqSvc, wantListRepo)
	jobH := NewJobHandler(jobRepo, sched, eventBus)
	wantListH := NewWantListHandler(wantListRepo, searchSvc, settingRepo)
	backlogH := NewBacklogHandler(backlogRepo, backlogSvc)
	calendarH := NewCalendarHandler(issueRepo, seriesRepo, wantListRepo, metaSvc, sched, searchSvc, settingRepo)
	organizeH := NewOrganizeHandler(organizeSvc, librarySvc)
	readerH := NewReaderHandler(readerSvc, fileRepo, issueRepo)
	indexerH := NewIndexerHandler(indexerRepo)
	dlClientH := NewDownloadClientHandler(dlClientRepo)
	searchH := NewSearchHandler(searchSvc, dlHistoryRepo, blocklistRepo, sched)
	metaWriterH := NewMetadataWriterHandler(metaWriterSvc)
	longboxMetadataH := NewLongboxMetadataHandler(seriesRepo, sched, longboxMetadataSvc, folderImageSvc)
	storyArcH := NewStoryArcHandler(storyArcRepo, metaSvc, cvClient)
	backupH := NewBackupHandler(backupSvc, settingRepo)
	opdsH := NewOPDSHandler(fileRepo, seriesRepo, coverSvc)
	authH := NewAuthHandler(authSvc)

	// Rate limiter: 5 attempts per minute for auth endpoints
	authLimiter := NewRateLimiter(5, 1*time.Minute)

	r.Route("/api/v1", func(r chi.Router) {
		// Soft 60-second deadline on every API request. ctx-aware handlers
		// abort instead of blocking forever behind a paused rate limiter or
		// a stalled upstream — that's the cause of the "Loading…" spinner
		// that never resolved when a long-running scan held the CV
		// limiter. SSE event stream is exempt (it's meant to be long-lived).
		r.Use(RequestTimeout(60*time.Second, "/api/v1/events"))

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
				r.Use(AdminOnlyWithAuth(authSvc))
				r.Get("/auth/users", authH.ListUsers)
				r.Post("/auth/users", authH.CreateUser)
				r.Delete("/auth/users/{id}", authH.DeleteUser)
				r.Post("/admin/backup", backupH.Create)
				r.Get("/admin/backups", backupH.List)
				r.Delete("/admin/backups/{name}", backupH.Delete)
				r.Get("/admin/backups/{name}/download", backupH.Download)
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
			r.Post("/library/scan/reconcile-cv", libraryH.ScanForceCV)
			r.Post("/admin/backfill-read-status", libraryH.BackfillReadStatus)
			r.Post("/admin/prune-want-list", libraryH.PruneWantList)
			r.Post("/admin/dedupe-issues", libraryH.DedupeIssues)
			r.Post("/admin/dedupe-series", libraryH.DedupeSeries)
			r.Post("/admin/dedupe-files", libraryH.DedupeFiles)
			r.Post("/admin/reorganize", libraryH.ReorganizeLibrary)
			r.Post("/admin/adopt-folders", libraryH.AdoptStrandedFolders)
			r.Post("/admin/reattach-orphans", libraryH.ReattachOrphanFiles)
			r.Get("/admin/pipeline-status", libraryH.PipelineStatus)
			r.Post("/admin/reconcile-backlog", libraryH.ReconcileBacklog)
			r.Get("/admin/test-search", libraryH.TestSearch)
			r.Get("/library/stats", libraryH.Stats)
			r.Get("/library/new-this-week", libraryH.NewThisWeek)
			r.Post("/library/write-longbox-metadata", longboxMetadataH.WriteAll)
			r.Post("/library/write-mylar-metadata", longboxMetadataH.WriteAll) // legacy alias
			r.Post("/series/{id}/write-longbox-metadata", longboxMetadataH.WriteForSeries)
			r.Post("/library/write-folder-images", longboxMetadataH.WriteAllFolderImages)
			r.Post("/series/{id}/write-folder-image", longboxMetadataH.WriteFolderImageForSeries)

			// Series
			r.Get("/series", seriesH.List)
			r.Get("/series/{id}", seriesH.Get)
			r.Get("/series/{id}/issues", seriesH.GetIssues)
			r.Put("/series/{id}/issues/skip-status", seriesH.BulkSetSkipStatus)
			r.Put("/series/{id}/link-annual", seriesH.LinkAnnual)
			r.Put("/series/{id}/unlink-annual", seriesH.UnlinkAnnual)
			r.Put("/series/{id}/track", seriesH.Track)
			r.Put("/series/{id}/untrack", seriesH.Untrack)
			r.Delete("/series/{id}/want-list", seriesH.ClearWantList)
			r.Delete("/series/{id}/issues", seriesH.DeleteAllIssues)
			r.Delete("/series/{id}", seriesH.Delete)
			r.Post("/series/{id}/merge-into/{dst_id}", seriesH.MergeInto)

			// Issues
			r.Get("/issues/{id}", issueH.Get)
			r.Put("/issues/{id}", issueH.UpdateMetadata)
			r.Delete("/issues/{id}", issueH.Delete)
			r.Put("/issues/{id}/read-status", issueH.UpdateReadStatus)
			r.Put("/issues/{id}/skip-status", issueH.UpdateSkipStatus)

			// Files
			r.Get("/files", fileH.List)
			r.Get("/files/duplicates", fileH.Duplicates)
			r.Post("/files/bulk-delete", fileH.BulkDelete)
			r.Post("/files/backfill-hashes", fileH.BackfillHashes)
			r.Get("/files/{id}", fileH.Get)
			r.Delete("/files/{id}", fileH.DeleteFile)
			r.Put("/files/{id}/rename", fileH.Rename)

			// Series files
			r.Get("/series/{id}/files", fileH.ListBySeries)

			// Covers
			r.Get("/covers/file/{id}", coverH.ServeFileCover)
			r.Get("/covers/proxy", coverH.ProxyURL)

			// Metadata (ComicVine)
			r.Get("/metadata/search", metaH.SearchVolumes)
			r.Get("/metadata/volume/{cvid}", metaH.GetVolume)
			r.Get("/metadata/volume/{cvid}/issues", metaH.GetVolumeIssues)
			r.Get("/metadata/story-arcs/search", storyArcH.Search)
			r.Post("/series/{id}/match", metaH.MatchSeries)
			r.Post("/series/{id}/refresh", metaH.RefreshSeries)

			// Metadata (Metron)
			r.Get("/metadata/metron/search", metaH.SearchMetron)
			r.Post("/series/{id}/match-metron", metaH.MatchSeriesToMetron)
			r.Post("/series/{id}/refresh-metron", metaH.RefreshSeriesFromMetron)

			// Story Arcs
			r.Get("/story-arcs", storyArcH.List)
			r.Get("/story-arcs/{id}", storyArcH.Get)
			r.Post("/story-arcs/import", storyArcH.Import)
			r.Delete("/story-arcs/{id}", storyArcH.Delete)

			// Settings
			r.Get("/settings", settingsH.GetSettings)
			r.Put("/settings/comicvine-api-key", settingsH.UpdateAPIKey)
			r.Post("/settings/comicvine-api-key/test", settingsH.TestAPIKey)
			r.Put("/settings/metron", settingsH.UpdateMetronCredentials)
			r.Post("/settings/metron/test", settingsH.TestMetron)
			r.Put("/settings/prowlarr", settingsH.UpdateProwlarrSettings)
			r.Post("/settings/prowlarr/test", settingsH.TestProwlarr)
			r.Put("/settings/library-dir", settingsH.UpdateLibraryDir)
			r.Put("/settings/pull-list-schedule", settingsH.UpdatePullListSchedule)
			r.Put("/settings/auto-search", settingsH.UpdateAutoSearch)
			r.Put("/settings/missing-search", settingsH.UpdateMissingSearch)
			r.Put("/settings/auto-scan", settingsH.UpdateAutoScan)
			r.Put("/settings/scan-reconcile", settingsH.UpdateScanReconcile)
			r.Put("/settings/post-process-script", settingsH.UpdatePostProcessScript)
			r.Put("/settings/backup", backupH.UpdateBackupSettings)
			r.Get("/settings/slack", settingsH.GetSlackSettings)
			r.Put("/settings/slack", settingsH.UpdateSlackSettings)
			r.Post("/settings/slack/test", settingsH.TestSlack)

			// Want List
			r.Get("/want-list", wantListH.List)
			r.Post("/want-list", wantListH.Add)
			r.Put("/want-list/{id}", wantListH.Update)
			r.Delete("/want-list/{id}", wantListH.Remove)

			// Backlog Runs
			r.Get("/backlog/runs", backlogH.ListRuns)
			r.Post("/backlog/runs", backlogH.CreateRun)
			r.Get("/backlog/items", backlogH.ListItems)
			r.Post("/backlog/runs/{id}/pause", backlogH.PauseRun)
			r.Post("/backlog/runs/{id}/resume", backlogH.ResumeRun)
			r.Post("/backlog/runs/{id}/retry-all", backlogH.RetryAllInRun)
			r.Post("/backlog/items/{id}/retry", backlogH.RetryItem)

			// Calendar / Pull List
			r.Get("/calendar", calendarH.Upcoming)
			r.Get("/calendar/releases", calendarH.Releases)
			r.Post("/calendar/refresh", calendarH.RefreshTracked)
			r.Post("/calendar/track", calendarH.TrackSeries)
			r.Post("/calendar/want", calendarH.WantIssue)
			r.Post("/pull-list/want-track", acquisitionH.WantTrack)

			// Wantlist (acquisition flow)
			r.Get("/wantlist", acquisitionH.ListWantlist)
			r.Post("/wantlist/{id}/retry", acquisitionH.RetryProcurement)

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
			r.Get("/search/blocklist", searchH.ListBlocklist)
			r.Delete("/search/blocklist/{id}", searchH.DeleteBlocklistEntry)
			r.Delete("/search/blocklist", searchH.ClearBlocklist)
			r.Post("/search/pull-list", searchH.TriggerPullListSearch)
			r.Get("/downloads", searchH.DownloadHistory)
		})
	})

	// OPDS catalog (no auth — OPDS clients typically don't support it)
	r.Route("/opds", func(r chi.Router) {
		r.Get("/", opdsH.Root)
		r.Get("/series", opdsH.SeriesCatalog)
		r.Get("/series/{id}", opdsH.SeriesIssues)
		r.Get("/recent", opdsH.Recent)
		r.Get("/search", opdsH.Search)
		r.Get("/file/{id}", opdsH.ServeFile)
		r.Get("/cover/{id}", opdsH.ServeCover)
	})

	// Serve embedded frontend (SPA with fallback)
	spaHandler := NewSPAHandler(frontendFS)
	r.NotFound(spaHandler.ServeHTTP)

	return r
}

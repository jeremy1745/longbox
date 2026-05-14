package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strconv"
	"time"

	longbox "github.com/jeremy/longbox"
	"github.com/jeremy/longbox/internal/comicvine"
	"github.com/jeremy/longbox/internal/metron"
	"github.com/jeremy/longbox/internal/config"
	"github.com/jeremy/longbox/internal/database"
	"github.com/jeremy/longbox/internal/handler"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/prowlarr"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scanner"
	"github.com/jeremy/longbox/internal/scheduler"
	"github.com/jeremy/longbox/internal/server"
	"github.com/jeremy/longbox/internal/service"
	"github.com/jeremy/longbox/internal/walksoftly"
)

func main() {
	configPath := flag.String("config", "", "path to config file")
	flag.Parse()

	// Load config
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	backlogSettings := service.BacklogSettings{
		DefaultIncludeVariants: cfg.Backlog.EnableVariants,
		MaxRetries:             cfg.Backlog.MaxRetries,
		RetryBackoffMinutes:    cfg.Backlog.RetryBackoffMinutes,
	}

	// Configure logging
	var logLevel slog.Level
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	// Open database
	db, err := database.Open(cfg.DatabasePath())
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Run migrations
	if err := db.Migrate(); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// Repositories
	fileRepo := repository.NewFileRepo(db.Read, db.Write)
	seriesRepo := repository.NewSeriesRepo(db.Read, db.Write)
	issueRepo := repository.NewIssueRepo(db.Read, db.Write)
	jobRepo := repository.NewJobRepo(db.Read, db.Write)
	if cleaned, err := jobRepo.CleanupOrphaned(); err != nil {
		slog.Warn("failed to clean up orphaned jobs", "error", err)
	} else if cleaned > 0 {
		slog.Info("cleaned up orphaned jobs from previous run", "count", cleaned)
	}
	wantListRepo := repository.NewWantListRepo(db.Read, db.Write)
	backlogRepo := repository.NewBacklogRepo(db.Read, db.Write)
	settingRepo := repository.NewSettingRepo(db.Read, db.Write)
	publisherRepo := repository.NewPublisherRepo(db.Read, db.Write)
	indexerRepo := repository.NewIndexerRepo(db.Read, db.Write)
	dlClientRepo := repository.NewDownloadClientRepo(db.Read, db.Write)
	dlHistoryRepo := repository.NewDownloadHistoryRepo(db.Read, db.Write)
	blocklistRepo := repository.NewBlocklistRepo(db.Read, db.Write)
	storyArcRepo := repository.NewStoryArcRepo(db.Read, db.Write)
	userRepo := repository.NewUserRepo(db.Read, db.Write)

	// External clients
	cvClient := comicvine.NewClient(cfg.ComicVineAPIKey)
	metronClient := metron.NewClient("", "")
	wsClient := walksoftly.NewClient()

	// Services
	coverSvc := service.NewCoverService(cfg.CoversDir(), fileRepo)
	librarySvc := service.NewLibraryService(cfg.LibraryDir, fileRepo, seriesRepo, issueRepo, wantListRepo, coverSvc)
	metaSvc := service.NewMetadataService(cvClient, metronClient, wsClient, seriesRepo, issueRepo, publisherRepo, wantListRepo, settingRepo, cfg.ComicVineAPIKey, cfg.LibraryDir)
	if err := metaSvc.EnsureAPIKey(); err != nil {
		slog.Warn("failed to load ComicVine API key from settings", "error", err)
	}
	if err := metaSvc.EnsureMetronCredentials(); err != nil {
		slog.Warn("failed to load Metron credentials from settings", "error", err)
	}

	// Load library dir from DB settings (overrides config file)
	if dbLibDir, err := settingRepo.Get("library_dir"); err == nil && dbLibDir != "" {
		librarySvc.SetLibraryDir(dbLibDir)
		slog.Info("loaded library directory from settings", "dir", dbLibDir)
	}

	// Prowlarr client — constructed from config, then overridden with any
	// persisted DB settings (same pattern as Metron credentials above).
	prowlarrClient := prowlarr.NewClient(cfg.ProwlarrURL, cfg.ProwlarrAPIKey, cfg.ProwlarrCategory)
	if dbURL, err := settingRepo.Get("prowlarr_url"); err == nil && dbURL != "" {
		dbKey, _ := settingRepo.Get("prowlarr_api_key")
		dbCat, _ := settingRepo.Get("prowlarr_category")
		prowlarrClient.SetConfig(dbURL, dbKey, dbCat)
		slog.Info("loaded Prowlarr settings from settings DB", "url", dbURL)
	}

	readerSvc := service.NewReaderService()
	organizeSvc := service.NewFileOrganizerService(fileRepo, issueRepo, seriesRepo, settingRepo, cfg.Backlog.AnnualSubfolder)
	metaWriterSvc := service.NewMetadataWriterService(fileRepo, issueRepo, seriesRepo)
	longboxMetadataSvc := service.NewLongboxMetadataService(seriesRepo, fileRepo, issueRepo, cvClient, librarySvc)
	folderImageSvc := service.NewFolderImageService(seriesRepo, fileRepo, librarySvc, coverSvc, metaSvc)
	backupSvc := service.NewBackupService(cfg.DatabasePath(), cfg.DataDir)

	// Run startup backup if enabled
	backupOnStart, _ := settingRepo.Get("backup_on_start")
	backupRetentionStr, _ := settingRepo.Get("backup_retention")
	backupRetention := 5
	if r, err := strconv.Atoi(backupRetentionStr); err == nil && r > 0 {
		backupRetention = r
	}
	backupSvc.RunStartupBackup(backupOnStart == "true", backupRetention)

	// Auth service
	authSvc := service.NewAuthService(userRepo, cfg.SessionLifetime())

	// Scheduler
	eventBus := scheduler.NewEventBus()
	sched := scheduler.NewScheduler(jobRepo, eventBus)

	// Search service (needs eventBus for SSE updates)
	searchSvc := service.NewSearchService(indexerRepo, dlClientRepo, dlHistoryRepo, issueRepo, seriesRepo, blocklistRepo, eventBus)
	backlogSvc := service.NewBacklogService(backlogRepo, issueRepo, seriesRepo, eventBus, backlogSettings)
	searchSvc.SetOnDownloadStatusChanged(backlogSvc.HandleDownloadStatus)

	backlogQueue := service.NewBacklogQueue(backlogRepo, searchSvc, backlogSvc, backlogSettings, cfg.Backlog.MaxConcurrentDownloads)
	backlogQueue.Start()
	// Lets backlog_service nudge the worker pool the moment new items land
	// (run created, retry-all clicked) instead of waiting on the 5s idle tick.
	backlogSvc.SetOnQueueDirty(backlogQueue.Wake)

	// Wire reconciliation deps onto LibraryService now that metaSvc + backlogSvc exist.
	librarySvc.SetReconcileDeps(settingRepo, metaSvc, backlogSvc)
	// Phase E poster refresh runs at the end of every scan so on-disk
	// catalog stays Mylar-shaped (one folder per series with cover.jpg).
	librarySvc.SetFolderImageService(folderImageSvc)

	// Import service for post-processing completed downloads
	importSvc := service.NewImportService(librarySvc, organizeSvc, wantListRepo, dlHistoryRepo, fileRepo, issueRepo, seriesRepo, settingRepo, cfg.LibraryDir)
	searchSvc.SetOnDownloadCompleted(importSvc.ImportCompletedDownload)

	// Acquisition flow services (Phase 3–6).
	// SeriesFolderService is wired here; it was previously orphaned (D1).
	libraryScanSvc := service.NewLibraryScanService(seriesRepo)
	seriesFolderSvc := service.NewSeriesFolderService(librarySvc, seriesRepo, issueRepo)
	// libraryDir for AcquisitionService: use librarySvc.GetLibraryDir() so that
	// a runtime-changed library dir (via PUT /settings/library-dir) is consistent
	// between SeriesFolderService (which calls GetLibraryDir() on every use) and
	// AcquisitionService (which snapshots the value at construction time). This is
	// the best we can do without making AcquisitionService call through librarySvc;
	// if the dir changes at runtime, a server restart will re-sync the snapshot.
	acqSvc := service.NewAcquisitionService(
		metaSvc,
		seriesFolderSvc,
		libraryScanSvc,
		prowlarrClient,
		seriesRepo,
		issueRepo,
		fileRepo,
		wantListRepo,
		librarySvc.GetLibraryDir(),
	)

	// Notification service (Slack webhooks)
	notifSvc := service.NewNotificationService(settingRepo, eventBus, dlHistoryRepo)
	notifSvc.Start()

	// Pull list service
	pullListSvc := service.NewPullListService(seriesRepo, issueRepo, wantListRepo, dlHistoryRepo, searchSvc, metaSvc)

	// Register job handlers
	sched.RegisterHandler(model.JobTypeScan, func(ctx context.Context, progress scheduler.ProgressFunc) error {
		_, err := librarySvc.ScanWithProgress(ctx, func(processed, total int, message string) {
			progress(processed, total, message)
		})
		return err
	})
	sched.RegisterHandler(model.JobTypeScanForceCV, func(ctx context.Context, progress scheduler.ProgressFunc) error {
		_, err := librarySvc.ScanWithOptions(ctx, service.ScanOptions{ForceCV: true}, func(processed, total int, message string) {
			progress(processed, total, message)
		})
		return err
	})
	sched.RegisterHandler(model.JobTypeMetadataRefresh, func(ctx context.Context, progress scheduler.ProgressFunc) error {
		_, _, err := metaSvc.RefreshTrackedSeries(ctx, func(processed, total int, message string) {
			progress(processed, total, message)
		})
		return err
	})
	sched.RegisterHandler(model.JobTypePullListSearch, func(ctx context.Context, progress scheduler.ProgressFunc) error {
		_, err := pullListSvc.RunWeeklySearch(ctx, func(processed, total int, message string) {
			progress(processed, total, message)
		})
		return err
	})
	sched.RegisterHandler(model.JobTypeMissingSearch, func(ctx context.Context, progress scheduler.ProgressFunc) error {
		_, err := pullListSvc.SearchMissing(ctx, func(processed, total int, message string) {
			progress(processed, total, message)
		})
		return err
	})
	sched.RegisterHandler(model.JobTypeHashBackfill, func(ctx context.Context, progress scheduler.ProgressFunc) error {
		_, _, err := librarySvc.BackfillHashes(ctx, func(processed, total int, message string) {
			progress(processed, total, message)
		})
		return err
	})
	sched.RegisterHandler(model.JobTypeLongboxMetadata, func(ctx context.Context, progress scheduler.ProgressFunc) error {
		_, _, _, err := longboxMetadataSvc.WriteAll(ctx, func(processed, total int, message string) {
			progress(processed, total, message)
		})
		return err
	})
	// JobTypeMylarMetadata is the legacy job-type string kept so old job rows
	// can still be replayed/displayed. New submissions go through
	// JobTypeLongboxMetadata. Without this guard a user could click the
	// legacy alias endpoint and queue a redundant identical pass.
	sched.RegisterHandler(model.JobTypeMylarMetadata, func(ctx context.Context, progress scheduler.ProgressFunc) error {
		slog.Info("legacy mylar_metadata job redirected to longbox_metadata")
		_, _, _, err := longboxMetadataSvc.WriteAll(ctx, func(processed, total int, message string) {
			progress(processed, total, message)
		})
		return err
	})
	sched.RegisterHandler(model.JobTypeFolderImages, func(ctx context.Context, progress scheduler.ProgressFunc) error {
		_, _, _, _, err := folderImageSvc.WriteAll(ctx, true, func(processed, total int, message string) {
			progress(processed, total, message)
		})
		return err
	})
	sched.RegisterHandler(model.JobTypeReorganize, func(ctx context.Context, progress scheduler.ProgressFunc) error {
		// Block the reorganize from starting if a scan is running on the
		// same library — scan opens each file for cover/hash extraction,
		// holding a Windows file handle that makes the reorg's os.Rename
		// fail with "Permission denied". The user sees zero progress and
		// no error message because the per-file rename failures are
		// counted internally but never surfaced to the job result.
		if sched.IsRunning(model.JobTypeScan) || sched.IsRunning(model.JobTypeScanForceCV) {
			return fmt.Errorf("a library scan is currently running; reorganize would race with it for file handles. Wait for the scan to finish or cancel it first")
		}
		result, err := organizeSvc.ExecuteWithProgress(ctx, librarySvc.GetLibraryDir(), func(processed, total int, message string) {
			progress(processed, total, message)
		})
		// Surface per-file move outcomes in the final progress message —
		// without this, "Completed in N seconds" hides the fact that 117
		// of 117 attempted moves failed because the files were locked.
		if result != nil {
			progress(result.TotalFiles, result.TotalFiles, fmt.Sprintf(
				"Reorganize: %d moved, %d skipped, %d errors",
				result.Moved, result.Skipped, result.Errors))
			if result.Errors > 0 && err == nil {
				if len(result.ErrorDetails) > 0 {
					return fmt.Errorf("reorganize: %d errors (first: %s)",
						result.Errors, result.ErrorDetails[0])
				}
				return fmt.Errorf("reorganize: %d errors (no detail captured)", result.Errors)
			}
		}
		return err
	})
	sched.RegisterHandler(model.JobTypeAdoptFolders, func(ctx context.Context, progress scheduler.ProgressFunc) error {
		_, err := librarySvc.AdoptStrandedFolders(ctx, func(processed, total int, message string) {
			progress(processed, total, message)
		})
		return err
	})

	// File watcher
	watcher, err := scanner.NewWatcher(cfg.LibraryDir, func(paths []string) {
		slog.Info("file changes detected", "count", len(paths))
	})
	if err != nil {
		slog.Warn("failed to create file watcher", "error", err)
	} else {
		if err := watcher.Start(); err != nil {
			slog.Warn("failed to start file watcher", "error", err)
		}
	}

	// Cron scheduler for periodic tasks
	cronSched := scheduler.NewCronScheduler(sched, settingRepo, searchSvc.CheckDownloadStatus)
	cronSched.Start()

	// Embedded frontend filesystem
	frontendFS, err := fs.Sub(longbox.FrontendFS, "ui/build")
	if err != nil {
		slog.Error("failed to load frontend", "error", err)
		os.Exit(1)
	}

	// Session cleanup goroutine
	sessionCleanupDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := authSvc.CleanExpiredSessions(); err != nil {
					slog.Warn("failed to clean expired sessions", "error", err)
				}
			case <-sessionCleanupDone:
				return
			}
		}
	}()

	// Create server (before router so we can pass it for shutdown support)
	srv := server.New(cfg.Port)

	// Router
	router := handler.NewRouter(
		fileRepo,
		seriesRepo,
		issueRepo,
		jobRepo,
		wantListRepo,
		backlogRepo,
		backlogSvc,
		indexerRepo,
		dlClientRepo,
		dlHistoryRepo,
		blocklistRepo,
		storyArcRepo,
		cvClient,
		librarySvc,
		coverSvc,
		metaSvc,
		organizeSvc,
		readerSvc,
		searchSvc,
		metaWriterSvc,
		longboxMetadataSvc,
		folderImageSvc,
		backupSvc,
		sched,
		eventBus,
		watcher,
		settingRepo,
		authSvc,
		srv,
		frontendFS,
		prowlarrClient,
		acqSvc,
	)

	// Start server
	srv.SetHandler(router)
	slog.Info("longbox starting", "port", cfg.Port, "library", cfg.LibraryDir)

	if err := srv.Start(); err != nil {
		slog.Error("server error", "error", err)
	}

	// Shutdown
	close(sessionCleanupDone)
	notifSvc.Stop()
	cronSched.Stop()
	backlogQueue.Stop()
	sched.Shutdown()
	if watcher != nil {
		watcher.Stop()
	}
}

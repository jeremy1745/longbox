package main

import (
	"context"
	"flag"
	"io/fs"
	"log/slog"
	"os"

	longbox "github.com/jeremy/longbox"
	"github.com/jeremy/longbox/internal/comicvine"
	"github.com/jeremy/longbox/internal/config"
	"github.com/jeremy/longbox/internal/database"
	"github.com/jeremy/longbox/internal/handler"
	"github.com/jeremy/longbox/internal/model"
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
	wantListRepo := repository.NewWantListRepo(db.Read, db.Write)
	settingRepo := repository.NewSettingRepo(db.Read, db.Write)
	publisherRepo := repository.NewPublisherRepo(db.Read, db.Write)
	indexerRepo := repository.NewIndexerRepo(db.Read, db.Write)
	dlClientRepo := repository.NewDownloadClientRepo(db.Read, db.Write)
	dlHistoryRepo := repository.NewDownloadHistoryRepo(db.Read, db.Write)

	// External clients
	cvClient := comicvine.NewClient(cfg.ComicVineAPIKey)
	wsClient := walksoftly.NewClient()

	// Services
	coverSvc := service.NewCoverService(cfg.CoversDir(), fileRepo)
	librarySvc := service.NewLibraryService(cfg.LibraryDir, fileRepo, seriesRepo, issueRepo, coverSvc)
	metaSvc := service.NewMetadataService(cvClient, wsClient, seriesRepo, issueRepo, publisherRepo, wantListRepo, settingRepo, cfg.ComicVineAPIKey, cfg.LibraryDir)
	readerSvc := service.NewReaderService()
	organizeSvc := service.NewFileOrganizerService(fileRepo, issueRepo, seriesRepo, settingRepo)
	metaWriterSvc := service.NewMetadataWriterService(fileRepo, issueRepo, seriesRepo)
	mylarSvc := service.NewMylarMetadataService(seriesRepo, fileRepo, cvClient)

	// Scheduler
	eventBus := scheduler.NewEventBus()
	sched := scheduler.NewScheduler(jobRepo, eventBus)

	// Search service (needs eventBus for SSE updates)
	searchSvc := service.NewSearchService(indexerRepo, dlClientRepo, dlHistoryRepo, issueRepo, seriesRepo, eventBus)

	// Pull list service
	pullListSvc := service.NewPullListService(seriesRepo, issueRepo, wantListRepo, dlHistoryRepo, searchSvc, metaSvc)

	// Register job handlers
	sched.RegisterHandler(model.JobTypeScan, func(ctx context.Context, progress scheduler.ProgressFunc) error {
		_, err := librarySvc.ScanWithProgress(ctx, func(processed, total int, message string) {
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
	sched.RegisterHandler(model.JobTypeMylarMetadata, func(ctx context.Context, progress scheduler.ProgressFunc) error {
		_, _, _, err := mylarSvc.WriteAll(ctx, func(processed, total int, message string) {
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

	// Router
	router := handler.NewRouter(
		fileRepo,
		seriesRepo,
		issueRepo,
		jobRepo,
		wantListRepo,
		indexerRepo,
		dlClientRepo,
		dlHistoryRepo,
		librarySvc,
		coverSvc,
		metaSvc,
		organizeSvc,
		readerSvc,
		searchSvc,
		metaWriterSvc,
		mylarSvc,
		sched,
		eventBus,
		watcher,
		settingRepo,
		frontendFS,
	)

	// Start server
	srv := server.New(cfg.Port, router)
	slog.Info("longbox starting", "port", cfg.Port, "library", cfg.LibraryDir)

	if err := srv.Start(); err != nil {
		slog.Error("server error", "error", err)
	}

	// Shutdown
	cronSched.Stop()
	sched.Shutdown()
	if watcher != nil {
		watcher.Stop()
	}
}

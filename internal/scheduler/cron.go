package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
)

// DownloadStatusChecker is called periodically to poll SABnzbd for status updates.
type DownloadStatusChecker func(ctx context.Context) error

// CronScheduler runs periodic jobs based on settings.
type CronScheduler struct {
	scheduler    *Scheduler
	settingRepo  *repository.SettingRepo
	statusChecker DownloadStatusChecker
	mu           sync.Mutex
	stopCh       chan struct{}
	running      bool
}

func NewCronScheduler(sched *Scheduler, settingRepo *repository.SettingRepo, statusChecker DownloadStatusChecker) *CronScheduler {
	return &CronScheduler{
		scheduler:     sched,
		settingRepo:   settingRepo,
		statusChecker: statusChecker,
		stopCh:        make(chan struct{}),
	}
}

// Start begins the cron loop.
func (cs *CronScheduler) Start() {
	cs.mu.Lock()
	if cs.running {
		cs.mu.Unlock()
		return
	}
	cs.running = true
	cs.mu.Unlock()

	go cs.run()
	slog.Info("cron scheduler started")
}

// Stop halts the cron loop.
func (cs *CronScheduler) Stop() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if !cs.running {
		return
	}
	close(cs.stopCh)
	cs.running = false
	slog.Info("cron scheduler stopped")
}

func (cs *CronScheduler) run() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-cs.stopCh:
			return
		case <-ticker.C:
			cs.tick()
		}
	}
}

func (cs *CronScheduler) tick() {
	// Check download statuses every tick
	if cs.statusChecker != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := cs.statusChecker(ctx); err != nil {
			slog.Warn("download status check failed", "error", err)
		}
		cancel()
	}

	// Check if pull list search should run
	cs.checkPullListSchedule()

	// Check if missing issue search should run
	cs.checkMissingSearch()

	// Check if automated library scan should run
	cs.checkAutoScan()
}

func (cs *CronScheduler) checkPullListSchedule() {
	enabled, _ := cs.settingRepo.Get("pull_list_enabled")
	if enabled != "true" {
		return
	}

	dayStr, _ := cs.settingRepo.Get("pull_list_day")
	hourStr, _ := cs.settingRepo.Get("pull_list_hour")

	// Defaults: Wednesday (3) at 6 AM
	targetDay := 3
	targetHour := 6

	if d, err := strconv.Atoi(dayStr); err == nil && d >= 0 && d <= 6 {
		targetDay = d
	}
	if h, err := strconv.Atoi(hourStr); err == nil && h >= 0 && h <= 23 {
		targetHour = h
	}

	now := time.Now()
	if int(now.Weekday()) != targetDay || now.Hour() != targetHour {
		return
	}

	// Check if already run today
	lastRun, _ := cs.settingRepo.Get("pull_list_last_run")
	today := now.Format("2006-01-02")
	if lastRun == today {
		return
	}

	// Submit the pull list search job
	slog.Info("triggering scheduled pull list search",
		"day", now.Weekday().String(),
		"hour", now.Hour(),
	)

	// Record the attempted run date BEFORE Submit so a "deferred" cron
	// (live user job already running) doesn't fire again the next tick.
	if err := cs.settingRepo.Set("pull_list_last_run", today); err != nil {
		slog.Warn("failed to record pull list last run", "error", err)
	}

	job, err := cs.scheduler.Submit(model.JobTypePullListSearch)
	if err != nil {
		slog.Info("pull list submit deferred (already running)", "error", err)
		return
	}

	slog.Info("scheduled pull list search submitted", "job_id", job.ID)
	_ = fmt.Sprintf("job %d", job.ID) // suppress unused import
}

func (cs *CronScheduler) checkMissingSearch() {
	enabled, _ := cs.settingRepo.Get("missing_search_enabled")
	if enabled != "true" {
		return
	}

	intervalStr, _ := cs.settingRepo.Get("missing_search_interval")
	interval := 10 // default: 10 minutes
	if i, err := strconv.Atoi(intervalStr); err == nil && i >= 1 && i <= 1440 {
		interval = i
	}

	// Check if enough time has elapsed since last run
	lastRunStr, _ := cs.settingRepo.Get("missing_search_last_run")
	if lastRunStr != "" {
		lastRun, err := time.Parse(time.RFC3339, lastRunStr)
		if err == nil && time.Since(lastRun) < time.Duration(interval)*time.Minute {
			return
		}
	}

	slog.Info("triggering missing issue search", "interval_min", interval)

	// Record the attempt timestamp BEFORE Submit. If Submit fails because
	// the same JobType is already running (e.g. user kicked off a manual
	// missing search that's still going), the cron has still "fired" for
	// this interval — it deferred to the live job. Without writing
	// last_run on the deferred path, the next tick would Submit again and
	// queue a duplicate run as soon as the user's job ends.
	if err := cs.settingRepo.Set("missing_search_last_run", time.Now().UTC().Format(time.RFC3339)); err != nil {
		slog.Warn("failed to record missing search last run", "error", err)
	}

	job, err := cs.scheduler.Submit(model.JobTypeMissingSearch)
	if err != nil {
		slog.Info("missing search submit deferred (already running)", "error", err)
		return
	}

	slog.Info("missing issue search submitted", "job_id", job.ID)
}

func (cs *CronScheduler) checkAutoScan() {
	enabled, _ := cs.settingRepo.Get("auto_scan_enabled")
	if enabled != "true" {
		return
	}

	intervalStr, _ := cs.settingRepo.Get("auto_scan_interval")
	interval := 60 // default: 60 minutes
	if i, err := strconv.Atoi(intervalStr); err == nil && i >= 5 && i <= 1440 {
		interval = i
	}

	lastRunStr, _ := cs.settingRepo.Get("auto_scan_last_run")
	if lastRunStr != "" {
		lastRun, err := time.Parse(time.RFC3339, lastRunStr)
		if err == nil && time.Since(lastRun) < time.Duration(interval)*time.Minute {
			return
		}
	}

	slog.Info("triggering automated library scan", "interval_min", interval)

	// Record attempt before Submit so a deferred run (user already
	// running a scan) doesn't immediately re-fire on the next tick.
	if err := cs.settingRepo.Set("auto_scan_last_run", time.Now().UTC().Format(time.RFC3339)); err != nil {
		slog.Warn("failed to record auto scan last run", "error", err)
	}

	job, err := cs.scheduler.Submit(model.JobTypeScan)
	if err != nil {
		slog.Info("auto scan submit deferred (already running)", "error", err)
		return
	}

	slog.Info("automated library scan submitted", "job_id", job.ID)
}

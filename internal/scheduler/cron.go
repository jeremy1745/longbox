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

	job, err := cs.scheduler.Submit(model.JobTypePullListSearch)
	if err != nil {
		slog.Warn("failed to submit pull list search job", "error", err)
		return
	}

	// Record that we ran today
	if err := cs.settingRepo.Set("pull_list_last_run", today); err != nil {
		slog.Warn("failed to record pull list last run", "error", err)
	}

	slog.Info("scheduled pull list search submitted", "job_id", job.ID)
	_ = fmt.Sprintf("job %d", job.ID) // suppress unused import
}

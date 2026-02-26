package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
)

// JobFunc is a function that executes a background job.
// It receives a context (cancelled on shutdown/cancel) and a progress callback.
type JobFunc func(ctx context.Context, progress ProgressFunc) error

// ProgressFunc is called by jobs to report progress.
type ProgressFunc func(processed, total int, message string)

// Scheduler manages background job execution.
type Scheduler struct {
	jobRepo  *repository.JobRepo
	eventBus *EventBus

	mu       sync.Mutex
	running  map[int64]context.CancelFunc // active job ID → cancel func
	handlers map[model.JobType]JobFunc
}

func NewScheduler(jobRepo *repository.JobRepo, eventBus *EventBus) *Scheduler {
	return &Scheduler{
		jobRepo:  jobRepo,
		eventBus: eventBus,
		running:  make(map[int64]context.CancelFunc),
		handlers: make(map[model.JobType]JobFunc),
	}
}

// RegisterHandler registers a handler function for a job type.
func (s *Scheduler) RegisterHandler(jobType model.JobType, fn JobFunc) {
	s.handlers[jobType] = fn
}

// Submit creates a new job and starts it in the background.
// Returns the job immediately (in pending/running state).
func (s *Scheduler) Submit(jobType model.JobType) (*model.Job, error) {
	handler, ok := s.handlers[jobType]
	if !ok {
		return nil, fmt.Errorf("no handler registered for job type %q", jobType)
	}

	// Check if a job of this type is already running
	s.mu.Lock()
	for id := range s.running {
		job, err := s.jobRepo.GetByID(id)
		if err == nil && job != nil && job.Type == jobType {
			s.mu.Unlock()
			return nil, fmt.Errorf("a %s job is already running (job #%d)", jobType, id)
		}
	}
	s.mu.Unlock()

	// Create job record
	job, err := s.jobRepo.Create(jobType)
	if err != nil {
		return nil, fmt.Errorf("creating job: %w", err)
	}

	// Broadcast job created event
	s.eventBus.Publish(Event{Type: "job:created", Data: job})

	// Run in background
	go s.execute(job.ID, jobType, handler)

	return job, nil
}

func (s *Scheduler) execute(jobID int64, jobType model.JobType, handler JobFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	// Register as running
	s.mu.Lock()
	s.running[jobID] = cancel
	s.mu.Unlock()

	// Cleanup on exit
	defer func() {
		s.mu.Lock()
		delete(s.running, jobID)
		s.mu.Unlock()
		cancel()
	}()

	// Mark as running
	if err := s.jobRepo.MarkRunning(jobID); err != nil {
		slog.Error("failed to mark job running", "job_id", jobID, "error", err)
		return
	}

	s.broadcastJobUpdate(jobID)

	slog.Info("job started", "job_id", jobID, "type", jobType)
	startTime := time.Now()

	// Create progress callback
	lastBroadcast := time.Now()
	progress := func(processed, total int, message string) {
		if err := s.jobRepo.UpdateProgress(jobID, processed, total, message); err != nil {
			slog.Warn("failed to update job progress", "job_id", jobID, "error", err)
		}
		// Throttle SSE broadcasts to at most every 500ms
		if time.Since(lastBroadcast) > 500*time.Millisecond {
			s.broadcastJobUpdate(jobID)
			lastBroadcast = time.Now()
		}
	}

	// Execute the handler
	err := handler(ctx, progress)

	duration := time.Since(startTime)

	if err != nil {
		if ctx.Err() == context.Canceled {
			slog.Info("job cancelled", "job_id", jobID, "type", jobType)
			s.jobRepo.MarkCancelled(jobID)
		} else {
			slog.Error("job failed", "job_id", jobID, "type", jobType, "error", err, "duration", duration)
			s.jobRepo.MarkFailed(jobID, err.Error())
		}
	} else {
		slog.Info("job completed", "job_id", jobID, "type", jobType, "duration", duration)
		s.jobRepo.MarkCompleted(jobID, fmt.Sprintf("Completed in %s", duration.Round(time.Millisecond)))
	}

	// Final broadcast
	s.broadcastJobUpdate(jobID)
}

func (s *Scheduler) broadcastJobUpdate(jobID int64) {
	job, err := s.jobRepo.GetByID(jobID)
	if err != nil || job == nil {
		return
	}
	s.eventBus.Publish(Event{Type: "job:updated", Data: job})
}

// Cancel stops a running job.
func (s *Scheduler) Cancel(jobID int64) error {
	s.mu.Lock()
	cancel, ok := s.running[jobID]
	s.mu.Unlock()

	if !ok {
		return fmt.Errorf("job %d is not running", jobID)
	}

	cancel()
	return nil
}

// IsRunning returns true if a job is currently executing.
func (s *Scheduler) IsRunning(jobType model.JobType) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.running {
		job, err := s.jobRepo.GetByID(id)
		if err == nil && job != nil && job.Type == jobType {
			return true
		}
	}
	return false
}

// ActiveCount returns the number of currently running jobs.
func (s *Scheduler) ActiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.running)
}

// Shutdown cancels all running jobs and waits briefly for them to finish.
func (s *Scheduler) Shutdown() {
	s.mu.Lock()
	for id, cancel := range s.running {
		slog.Info("cancelling job for shutdown", "job_id", id)
		cancel()
	}
	s.mu.Unlock()

	// Give jobs a moment to clean up
	time.Sleep(500 * time.Millisecond)
}

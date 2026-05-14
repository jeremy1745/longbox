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

type queuedJob struct {
	id      int64
	jobType model.JobType
	handler JobFunc
}

// Scheduler manages background job execution with a serial queue.
// Only one job runs at a time; additional submissions are queued.
type Scheduler struct {
	jobRepo  *repository.JobRepo
	eventBus *EventBus

	mu            sync.Mutex
	running       map[int64]context.CancelFunc // active job ID → cancel func
	pendingByType map[model.JobType]int64
	handlers      map[model.JobType]JobFunc

	queue  chan queuedJob
	stopCh chan struct{}
}

func NewScheduler(jobRepo *repository.JobRepo, eventBus *EventBus) *Scheduler {
	s := &Scheduler{
		jobRepo:       jobRepo,
		eventBus:      eventBus,
		running:       make(map[int64]context.CancelFunc),
		pendingByType: make(map[model.JobType]int64),
		handlers:      make(map[model.JobType]JobFunc),
		queue:         make(chan queuedJob, 100),
		stopCh:        make(chan struct{}),
	}
	go s.processQueue()
	return s
}

// RegisterHandler registers a handler function for a job type.
// Takes s.mu to keep the handlers map race-free even if a future caller
// registers concurrently with Submit (today RegisterHandler is only invoked
// at startup, but it's exported, so the lock is cheap insurance).
func (s *Scheduler) RegisterHandler(jobType model.JobType, fn JobFunc) {
	s.mu.Lock()
	s.handlers[jobType] = fn
	s.mu.Unlock()
}

// Submit creates a new job and queues it for execution.
// Returns the job immediately in pending state.
func (s *Scheduler) Submit(jobType model.JobType) (*model.Job, error) {
	s.mu.Lock()
	handler, ok := s.handlers[jobType]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("no handler registered for job type %q", jobType)
	}

	// Check if a job of this type is already queued or running. The
	// pendingByType guard rejects a duplicate before it reaches the queue;
	// the s.running scan catches one that's already executing. (s.mu is
	// already held from the top of Submit.)
	if pendingID, ok := s.pendingByType[jobType]; ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("a %s job is already queued (job #%d)", jobType, pendingID)
	}
	for id := range s.running {
		job, err := s.jobRepo.GetByID(id)
		if err == nil && job != nil && job.Type == jobType {
			s.mu.Unlock()
			return nil, fmt.Errorf("a %s job is already running (job #%d)", jobType, id)
		}
	}
	s.mu.Unlock()

	// Create job record (starts as pending)
	job, err := s.jobRepo.Create(jobType)
	if err != nil {
		return nil, fmt.Errorf("creating job: %w", err)
	}

	// Broadcast job created event
	s.eventBus.Publish(Event{Type: "job:created", Data: job})

	// Record the job under its type so a duplicate Submit is rejected by
	// the pendingByType guard above, then queue it. Non-blocking send: a
	// blocking send on a saturated queue would wedge the caller (HTTP
	// handler, cron tick) indefinitely — instead mark the job failed and
	// surface the reason. On queue-full, drop the pendingByType entry we
	// just set so the type isn't permanently blocked.
	s.mu.Lock()
	s.pendingByType[jobType] = job.ID
	s.mu.Unlock()
	select {
	case s.queue <- queuedJob{id: job.ID, jobType: jobType, handler: handler}:
		return job, nil
	default:
		s.mu.Lock()
		delete(s.pendingByType, jobType)
		s.mu.Unlock()
		if err := s.jobRepo.MarkFailed(job.ID, "scheduler queue full"); err != nil {
			slog.Warn("failed to mark queue-full job failed", "job_id", job.ID, "error", err)
		}
		s.broadcastJobUpdate(job.ID)
		return nil, fmt.Errorf("scheduler queue full (capacity %d)", cap(s.queue))
	}
}

// processQueue runs jobs one at a time from the queue.
func (s *Scheduler) processQueue() {
	for {
		select {
		case <-s.stopCh:
			return
		case queued := <-s.queue:
			s.mu.Lock()
			delete(s.pendingByType, queued.jobType)
			s.mu.Unlock()

			// Check if the job was cancelled while waiting in the queue
			job, err := s.jobRepo.GetByID(queued.id)
			if err != nil || job == nil || job.Status == model.JobStatusCancelled {
				continue
			}
			s.execute(queued.id, queued.jobType, queued.handler)
		}
	}
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

	// Check if the job was already cancelled (by Cancel()) before writing final status
	currentJob, _ := s.jobRepo.GetByID(jobID)
	alreadyCancelled := currentJob != nil && currentJob.Status == model.JobStatusCancelled

	if alreadyCancelled {
		slog.Info("job cancelled", "job_id", jobID, "type", jobType)
	} else if err != nil {
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

// Cancel stops a running job, or removes a pending job from the queue.
func (s *Scheduler) Cancel(jobID int64) error {
	s.mu.Lock()
	cancel, ok := s.running[jobID]
	s.mu.Unlock()

	if job, err := s.jobRepo.GetByID(jobID); err == nil && job != nil {
		s.mu.Lock()
		if pendingID, exists := s.pendingByType[job.Type]; exists && pendingID == jobID {
			delete(s.pendingByType, job.Type)
		}
		s.mu.Unlock()
	}

	// Mark cancelled in the DB immediately so the UI reflects it
	if err := s.jobRepo.MarkCancelled(jobID); err != nil {
		slog.Warn("failed to mark job cancelled", "job_id", jobID, "error", err)
	}
	s.broadcastJobUpdate(jobID)

	if ok {
		// Signal the running goroutine to stop
		cancel()
	}
	// If not running, it was pending in the queue — the DB status is now
	// cancelled, so processQueue will skip it when it's dequeued.

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

// Shutdown cancels all running jobs and stops the queue processor.
func (s *Scheduler) Shutdown() {
	close(s.stopCh)

	s.mu.Lock()
	for id, cancel := range s.running {
		slog.Info("cancelling job for shutdown", "job_id", id)
		cancel()
	}
	s.mu.Unlock()

	// Give jobs a moment to clean up
	time.Sleep(500 * time.Millisecond)
}

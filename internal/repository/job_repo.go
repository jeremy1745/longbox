package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jeremy/longbox/internal/model"
)

type JobRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewJobRepo(read, write *sql.DB) *JobRepo {
	return &JobRepo{read: read, write: write}
}

// Create inserts a new job and returns it with the assigned ID.
func (r *JobRepo) Create(jobType model.JobType) (*model.Job, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.write.Exec(`
		INSERT INTO jobs (type, status, progress, total_items, processed_items, created_at)
		VALUES (?, ?, 0, 0, 0, ?)`,
		string(jobType), string(model.JobStatusPending), now,
	)
	if err != nil {
		return nil, fmt.Errorf("creating job: %w", err)
	}
	id, _ := res.LastInsertId()

	return &model.Job{
		ID:     id,
		Type:   jobType,
		Status: model.JobStatusPending,
	}, nil
}

// GetByID fetches a job by ID.
func (r *JobRepo) GetByID(id int64) (*model.Job, error) {
	row := r.read.QueryRow(`
		SELECT id, type, status, progress, total_items, processed_items,
			COALESCE(message,''), started_at, completed_at, created_at
		FROM jobs WHERE id = ?`, id)
	return scanJob(row)
}

// MarkRunning sets a job to running status.
func (r *JobRepo) MarkRunning(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.write.Exec(`
		UPDATE jobs SET status = ?, started_at = ? WHERE id = ?`,
		string(model.JobStatusRunning), now, id)
	return err
}

// UpdateProgress updates progress counters on a running job.
func (r *JobRepo) UpdateProgress(id int64, processed, total int, message string) error {
	progress := 0
	if total > 0 {
		progress = (processed * 100) / total
	}
	_, err := r.write.Exec(`
		UPDATE jobs SET progress = ?, processed_items = ?, total_items = ?, message = ?
		WHERE id = ?`,
		progress, processed, total, message, id)
	return err
}

// MarkCompleted sets a job to completed status.
func (r *JobRepo) MarkCompleted(id int64, message string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.write.Exec(`
		UPDATE jobs SET status = ?, progress = 100, completed_at = ?, message = ?
		WHERE id = ?`,
		string(model.JobStatusCompleted), now, message, id)
	return err
}

// MarkFailed sets a job to failed status.
func (r *JobRepo) MarkFailed(id int64, message string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.write.Exec(`
		UPDATE jobs SET status = ?, completed_at = ?, message = ?
		WHERE id = ?`,
		string(model.JobStatusFailed), now, message, id)
	return err
}

// MarkCancelled sets a job to cancelled status.
func (r *JobRepo) MarkCancelled(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.write.Exec(`
		UPDATE jobs SET status = ?, completed_at = ?, message = 'Cancelled by user'
		WHERE id = ?`,
		string(model.JobStatusCancelled), now, id)
	return err
}

// List returns recent jobs, most recent first.
func (r *JobRepo) List(limit int) ([]model.Job, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.read.Query(`
		SELECT id, type, status, progress, total_items, processed_items,
			COALESCE(message,''), started_at, completed_at, created_at
		FROM jobs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("listing jobs: %w", err)
	}
	defer rows.Close()

	var jobs []model.Job
	for rows.Next() {
		j, err := scanJobRow(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *j)
	}
	return jobs, nil
}

// ActiveJobs returns all currently running jobs.
func (r *JobRepo) ActiveJobs() ([]model.Job, error) {
	rows, err := r.read.Query(`
		SELECT id, type, status, progress, total_items, processed_items,
			COALESCE(message,''), started_at, completed_at, created_at
		FROM jobs WHERE status IN (?, ?) ORDER BY id DESC`,
		string(model.JobStatusPending), string(model.JobStatusRunning))
	if err != nil {
		return nil, fmt.Errorf("listing active jobs: %w", err)
	}
	defer rows.Close()

	var jobs []model.Job
	for rows.Next() {
		j, err := scanJobRow(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *j)
	}
	return jobs, nil
}

func scanJob(row *sql.Row) (*model.Job, error) {
	j := &model.Job{}
	var startedAt, completedAt, createdAt sql.NullString
	err := row.Scan(
		&j.ID, &j.Type, &j.Status, &j.Progress, &j.TotalItems, &j.ProcessedItems,
		&j.Message, &startedAt, &completedAt, &createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning job: %w", err)
	}
	if startedAt.Valid {
		t, _ := time.Parse(time.RFC3339, startedAt.String)
		j.StartedAt = &t
	}
	if completedAt.Valid {
		t, _ := time.Parse(time.RFC3339, completedAt.String)
		j.CompletedAt = &t
	}
	if createdAt.Valid {
		j.CreatedAt, _ = time.Parse(time.RFC3339, createdAt.String)
	}
	return j, nil
}

func scanJobRow(rows *sql.Rows) (*model.Job, error) {
	j := &model.Job{}
	var startedAt, completedAt, createdAt sql.NullString
	err := rows.Scan(
		&j.ID, &j.Type, &j.Status, &j.Progress, &j.TotalItems, &j.ProcessedItems,
		&j.Message, &startedAt, &completedAt, &createdAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning job row: %w", err)
	}
	if startedAt.Valid {
		t, _ := time.Parse(time.RFC3339, startedAt.String)
		j.StartedAt = &t
	}
	if completedAt.Valid {
		t, _ := time.Parse(time.RFC3339, completedAt.String)
		j.CompletedAt = &t
	}
	if createdAt.Valid {
		j.CreatedAt, _ = time.Parse(time.RFC3339, createdAt.String)
	}
	return j, nil
}

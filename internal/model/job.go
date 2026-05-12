package model

import "time"

// JobType identifies the kind of background job.
type JobType string

const (
	JobTypeScan           JobType = "scan"
	JobTypeMetadataRefresh JobType = "metadata_refresh"
	JobTypeSearch          JobType = "search"
	JobTypePullListSearch  JobType = "pull_list_search"
	JobTypeMylarMetadata   JobType = "mylar_metadata"
	JobTypeMissingSearch   JobType = "missing_search"
	JobTypeHashBackfill    JobType = "hash_backfill"
)

// JobStatus tracks the lifecycle of a job.
type JobStatus string

const (
	JobStatusPending   JobStatus = "pending"
	JobStatusRunning   JobStatus = "running"
	JobStatusCompleted JobStatus = "completed"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCancelled JobStatus = "cancelled"
)

type Job struct {
	ID             int64      `json:"id"`
	Type           JobType    `json:"type"`
	Status         JobStatus  `json:"status"`
	Progress       int        `json:"progress"`
	TotalItems     int        `json:"total_items"`
	ProcessedItems int        `json:"processed_items"`
	Message        string     `json:"message,omitempty"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	CompletedAt    *time.Time `json:"completed_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

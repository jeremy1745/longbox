package model

import "time"

// DownloadStatus tracks the lifecycle of a download.
type DownloadStatus string

const (
	DownloadStatusGrabbed      DownloadStatus = "grabbed"
	DownloadStatusDownloading  DownloadStatus = "downloading"
	DownloadStatusCompleted    DownloadStatus = "completed"
	DownloadStatusFailed       DownloadStatus = "failed"
	DownloadStatusImportFailed DownloadStatus = "import_failed"
)

// DownloadHistoryItem represents a grabbed/downloaded NZB.
type DownloadHistoryItem struct {
	ID               int64          `json:"id"`
	IssueID          *int64         `json:"issue_id,omitempty"`
	IndexerID        *int64         `json:"indexer_id,omitempty"`
	DownloadClientID *int64         `json:"download_client_id,omitempty"`
	NZBName          string         `json:"nzb_name"`
	NZBURL           string         `json:"nzb_url,omitempty"`
	ExternalID       string         `json:"external_id,omitempty"`
	Status           DownloadStatus `json:"status"`
	Size             int64          `json:"size"`
	Message          string         `json:"message,omitempty"`
	GrabbedAt        string         `json:"grabbed_at"`
	CompletedAt      *string        `json:"completed_at,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`

	// Joined fields for display
	SeriesTitle string `json:"series_title,omitempty"`
	IssueNumber string `json:"issue_number,omitempty"`
	IndexerName string `json:"indexer_name,omitempty"`
}

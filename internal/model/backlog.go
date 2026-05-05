package model

// BacklogRun represents a bulk backlog queue request for a single series.
type BacklogRun struct {
	ID              int64  `json:"id"`
	SeriesID        int64  `json:"series_id"`
	SeriesTitle     string `json:"series_title"`
	Status          string `json:"status"`
	IncludeVariants bool   `json:"include_variants"`
	TotalIssues     int    `json:"total_issues"`
	QueuedIssues    int    `json:"queued_issues"`
	CompletedIssues int    `json:"completed_issues"`
	FailedIssues    int    `json:"failed_issues"`
	Paused          bool   `json:"paused"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// BacklogItem represents an individual issue (or variant) being processed via backlog automation.
type BacklogItem struct {
	ID                int64  `json:"id"`
	BacklogRunID      int64  `json:"backlog_run_id"`
	SeriesID          int64  `json:"series_id"`
	IssueID           int64  `json:"issue_id"`
	VariantName       string `json:"variant_name"`
	Priority          int    `json:"priority"`
	Status            string `json:"status"`
	RetryCount        int    `json:"retry_count"`
	RetryAt           string `json:"retry_at,omitempty"`
	LastError         string `json:"last_error,omitempty"`
	SabNzoID          string `json:"sab_nzo_id,omitempty"`
	NZBGuid           string `json:"nzb_guid,omitempty"`
	DownloadHistoryID *int64 `json:"download_history_id,omitempty"`
	IssueNumber       string `json:"issue_number,omitempty"`
	SeriesTitle       string `json:"series_title,omitempty"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

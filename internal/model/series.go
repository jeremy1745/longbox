package model

import "time"

type Series struct {
	ID             int64     `json:"id"`
	Title          string    `json:"title"`
	SortTitle      string    `json:"sort_title"`
	Year           *int      `json:"year,omitempty"`
	PublisherID    *int64    `json:"publisher_id,omitempty"`
	ComicVineID    *int64    `json:"comicvine_id,omitempty"`
	MetronID       *int64    `json:"metron_id,omitempty"`
	MetronModified *string   `json:"metron_modified_at,omitempty"`
	Description    string    `json:"description,omitempty"`
	Status         string    `json:"status"`
	TotalIssues    int       `json:"total_issues"`
	CoverFileID    *int64    `json:"cover_file_id,omitempty"`
	Tracked        bool      `json:"tracked"`
	MetadataLocked bool      `json:"metadata_locked"`
	LastCVSync     *string   `json:"last_cv_sync,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// Parent series linking (annuals/specials)
	ParentSeriesID *int64   `json:"parent_series_id,omitempty"`

	// Computed fields (not stored directly)
	IssueCount     int      `json:"issue_count"`
	FileCount      int      `json:"file_count"`
	PublisherName  string   `json:"publisher_name,omitempty"`
	AnnualSeries   []Series `json:"annual_series,omitempty"`
}

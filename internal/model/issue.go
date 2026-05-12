package model

import "time"

type Issue struct {
	ID             int64     `json:"id"`
	SeriesID       int64     `json:"series_id"`
	IssueNumber    string    `json:"issue_number"`
	SortNumber     float64   `json:"sort_number"`
	Title          string    `json:"title,omitempty"`
	ComicVineID    *int64    `json:"comicvine_id,omitempty"`
	Description    string    `json:"description,omitempty"`
	CoverDate      string    `json:"cover_date,omitempty"`
	StoreDate      string    `json:"store_date,omitempty"`
	CoverURL       string    `json:"cover_url,omitempty"`
	Writers        string    `json:"writers,omitempty"`
	Artists        string    `json:"artists,omitempty"`
	ReadStatus     string    `json:"read_status"`
	SkipStatus     *string   `json:"skip_status,omitempty"`
	Rating         *int      `json:"rating,omitempty"`
	LastReadPage   *int      `json:"last_read_page,omitempty"`
	MetadataLocked bool      `json:"metadata_locked"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// Computed fields
	HasFile      bool   `json:"has_file"`
	FileID       *int64 `json:"file_id,omitempty"`
	SeriesTitle  string `json:"series_title,omitempty"`
}

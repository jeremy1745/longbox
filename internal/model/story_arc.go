package model

import "time"

// StoryArc represents a story arc (crossover, reading order).
type StoryArc struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	ComicVineID *int64    `json:"comicvine_id,omitempty"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`

	// Computed fields
	IssueCount int `json:"issue_count"`
	OwnedCount int `json:"owned_count"`
}

// StoryArcIssue represents an issue within a story arc with joined data.
type StoryArcIssue struct {
	StoryArcID     int64  `json:"story_arc_id"`
	IssueID        int64  `json:"issue_id"`
	SequenceNumber *int   `json:"sequence_number,omitempty"`

	// Joined fields
	SeriesTitle  string `json:"series_title,omitempty"`
	IssueNumber  string `json:"issue_number,omitempty"`
	CoverURL     string `json:"cover_url,omitempty"`
	HasFile      bool   `json:"has_file"`
	ReadStatus   string `json:"read_status,omitempty"`
}

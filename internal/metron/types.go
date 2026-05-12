// Package metron talks to the Metron Comic Book Database REST API
// (https://metron.cloud/api/) as a second metadata source alongside
// ComicVine.
//
// Auth: HTTP Basic with the user's metron.cloud username + the personal
// API token issued under their account profile.
//
// Rate limits: 20 requests per minute (burst) + 5000 per day (sustained).
// The default RateLimiter in this package enforces both with a small
// safety margin.
package metron

import "time"

// SearchResult is a thin volume/series row used by the search merge UI.
// Mirrors comicvine.SearchResult so MetadataService can union them
// without exposing source-specific shapes.
type SearchResult struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	YearStarted   int    `json:"year_started,omitempty"`
	IssueCount    int    `json:"issue_count,omitempty"`
	Description   string `json:"description,omitempty"`
	ImageURL      string `json:"image_url,omitempty"`
	PublisherName string `json:"publisher_name,omitempty"`
}

// Series is the detailed series payload returned by /series/{id}/.
// Only fields LongBox consumes today are mapped — the Metron schema is
// larger than this struct.
type Series struct {
	ID            int       `json:"id"`
	Name          string    `json:"name"`
	SortName      string    `json:"sort_name,omitempty"`
	YearStarted   int       `json:"year_began"`
	YearEnded     *int      `json:"year_end,omitempty"`
	IssueCount    int       `json:"issue_count"`
	Description   string    `json:"desc,omitempty"`
	ImageURL      string    `json:"image,omitempty"`
	PublisherID   int       `json:"publisher,omitempty"`
	PublisherName string    `json:"publisher_name,omitempty"`
	ModifiedAt    time.Time `json:"modified,omitempty"`
}

// Issue is the detailed issue payload returned by /issue/{id}/.
type Issue struct {
	ID         int       `json:"id"`
	SeriesID   int       `json:"series,omitempty"`
	Number     string    `json:"number"`
	Name       string    `json:"name,omitempty"`
	StoreDate  string    `json:"store_date,omitempty"`
	CoverDate  string    `json:"cover_date,omitempty"`
	ImageURL   string    `json:"image,omitempty"`
	Description string   `json:"desc,omitempty"`
	ModifiedAt time.Time `json:"modified,omitempty"`
}

// paginated wraps Metron's standard DRF response envelope.
type paginated[T any] struct {
	Count    int  `json:"count"`
	Next     *string `json:"next"`
	Previous *string `json:"previous"`
	Results  []T  `json:"results"`
}

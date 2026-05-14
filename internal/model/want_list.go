package model

// WantListItem represents an issue on the user's want list.
type WantListItem struct {
	ID          int64  `json:"id"`
	IssueID     int64  `json:"issue_id"`
	Priority    int    `json:"priority"`
	Notes       string `json:"notes,omitempty"`
	AddedAt     string `json:"added_at"`

	// Procurement tracking fields (added in migration 015).
	ProcurementStatus      string  `json:"procurement_status"`
	ProcurementSubmittedAt *string `json:"procurement_submitted_at,omitempty"`
	ProcurementLastError   *string `json:"procurement_last_error,omitempty"`

	// Joined fields from issues + series
	IssueNumber string `json:"issue_number"`
	SeriesID    int64  `json:"series_id"`
	SeriesTitle string `json:"series_title"`
	CoverURL    string `json:"cover_url,omitempty"`
	StoreDate   string `json:"store_date,omitempty"`
	CoverDate   string `json:"cover_date,omitempty"`
}

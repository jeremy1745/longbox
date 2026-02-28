package walksoftly

// Release represents a single comic issue from the walksoftly weekly release service.
// This service aggregates weekly comic shipping data with pre-matched ComicVine IDs.
type Release struct {
	Publisher  string  `json:"publisher"`
	Series     string  `json:"series"`
	Issue      string  `json:"issue"`
	ComicID    *string `json:"comicid"`    // ComicVine volume ID (nullable)
	IssueID    *string `json:"issueid"`    // ComicVine issue ID (nullable)
	ShipDate   string  `json:"shipdate"`   // YYYY-MM-DD
	CoverDate  *string `json:"coverdate"`  // YYYY-MM-DD (nullable)
	Alias      *string `json:"alias"`      // Alternative series name
	WeekNumber string  `json:"weeknumber"` // Sunday-based week number
	Year       string  `json:"year"`
	Volume     *string `json:"volume"`     // Volume number
	SeriesYear *string `json:"seriesyear"` // Year series started
	Format     *string `json:"type"`       // Comic format type
	Link       *string `json:"link"`       // Annual/special link
	Current    *string `json:"current"`    // "1" if current series
	LastIssue  *string `json:"lastissue"`  // Previous issue number
	Forced     string  `json:"forced"`     // Override flag
}

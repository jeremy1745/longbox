package comicvine

// APIResponse wraps all ComicVine API responses.
type APIResponse[T any] struct {
	Error                string `json:"error"`
	Limit                int    `json:"limit"`
	Offset               int    `json:"offset"`
	NumberOfPageResults  int    `json:"number_of_page_results"`
	NumberOfTotalResults int    `json:"number_of_total_results"`
	StatusCode           int    `json:"status_code"`
	Results              T      `json:"results"`
}

// Volume represents a comic series (ComicVine calls them "volumes").
type Volume struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	StartYear   string  `json:"start_year"`
	Description string  `json:"description"`
	CountOfIssues int   `json:"count_of_issues"`
	Publisher   *Publisher `json:"publisher"`
	Image       *Image  `json:"image"`
	SiteURL     string  `json:"site_detail_url"`
	Issues      []IssueRef `json:"issues"`
}

// IssueRef is a brief issue reference within a volume listing.
type IssueRef struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	IssueNumber string `json:"issue_number"`
	SiteURL     string `json:"site_detail_url"`
}

// Issue represents a single comic issue from ComicVine.
type Issue struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	IssueNumber string `json:"issue_number"`
	Description string `json:"description"`
	CoverDate   string `json:"cover_date"`
	StoreDate   string `json:"store_date"`
	Image       *Image `json:"image"`
	SiteURL     string `json:"site_detail_url"`
	Volume      *VolumeRef `json:"volume"`
	PersonCredits []PersonCredit `json:"person_credits"`
}

// VolumeRef is a brief volume reference within an issue.
type VolumeRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Publisher represents a comic publisher.
type Publisher struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// Image holds the various image sizes from ComicVine.
type Image struct {
	IconURL        string `json:"icon_url"`
	MediumURL      string `json:"medium_url"`
	ScreenURL      string `json:"screen_url"`
	ScreenLargeURL string `json:"screen_large_url"`
	SmallURL       string `json:"small_url"`
	SuperURL       string `json:"super_url"`
	ThumbURL       string `json:"thumb_url"`
	TinyURL        string `json:"tiny_url"`
	OriginalURL    string `json:"original_url"`
}

// PersonCredit represents a creator credit on an issue.
type PersonCredit struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Role string `json:"role"`
}

// SearchResult is the format for search results.
type SearchResult struct {
	ID          int        `json:"id"`
	Name        string     `json:"name"`
	StartYear   string     `json:"start_year"`
	CountOfIssues int      `json:"count_of_issues"`
	Description string     `json:"description"`
	Publisher   *Publisher `json:"publisher"`
	Image       *Image     `json:"image"`
	ResourceType string   `json:"resource_type"`
}

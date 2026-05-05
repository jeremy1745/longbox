// Package metron is a client for the Metron Comic Database REST API.
//
// API docs: https://metron.cloud/docs/
// Best-practices reference: https://metron-project.github.io/blog/api-best-practices
//
// The package follows Metron's published rules:
//   - HTTP Basic Auth via the Authorization header (never in query strings).
//   - Read X-RateLimit-* response headers and pause before remaining hits zero.
//   - Walk paginated results sequentially via the next URL the server returns.
//   - Use If-Modified-Since on detail endpoints to avoid burning quota.
//   - Filter server-side; never fetch a list and filter locally.
//   - Don't retry 4xx responses (except 429).
package metron

// GenericItem is the {id, name} pair Metron uses for nested references
// (publisher, series_type, rating, role, genre, etc.).
type GenericItem struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// BaseResource is Metron's lightweight {id, name} for list-style nested
// references (arcs, characters, teams, universes).
type BaseResource struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// ListResponse mirrors Django REST Framework's paginated response. Next and
// Previous are absolute URLs ready to pass back into the client.
type ListResponse[T any] struct {
	Count    int    `json:"count"`
	Next     string `json:"next,omitempty"`
	Previous string `json:"previous,omitempty"`
	Results  []T    `json:"results"`
}

// SeriesListItem is the lightweight series row returned by /api/series/ (and
// the basis for search results).
type SeriesListItem struct {
	ID         int    `json:"id"`
	Display    string `json:"series"` // e.g. "Ultimate Wolverine (2025)"
	YearBegan  int    `json:"year_began"`
	IssueCount int    `json:"issue_count"`
	Volume     int    `json:"volume"`
	Modified   string `json:"modified"`
}

// Series is the detail-endpoint payload for /api/series/{id}/.
type Series struct {
	ID          int           `json:"id"`
	Name        string        `json:"name"`
	SortName    string        `json:"sort_name"`
	Volume      int           `json:"volume"`
	YearBegan   int           `json:"year_began"`
	YearEnd     *int          `json:"year_end"`
	SeriesType  GenericItem   `json:"series_type"`
	Status      string        `json:"status"`
	Publisher   GenericItem   `json:"publisher"`
	Imprint     *GenericItem  `json:"imprint"`
	Description string        `json:"desc"`
	Genres      []GenericItem `json:"genres"`
	CVID        *int          `json:"cv_id"`
	GCDID       *int          `json:"gcd_id"`
	ResourceURL string        `json:"resource_url"`
	IssueCount  int           `json:"issue_count"`
	Modified    string        `json:"modified"`
}

// IssueSeries is the inline series block embedded in /api/issue/{id}/.
type IssueSeries struct {
	ID         int           `json:"id"`
	Name       string        `json:"name"`
	SortName   string        `json:"sort_name"`
	Volume     int           `json:"volume"`
	YearBegan  int           `json:"year_began"`
	SeriesType GenericItem   `json:"series_type"`
	Genres     []GenericItem `json:"genres"`
}

// IssueListItem is the lightweight row from /api/issue/.
type IssueListItem struct {
	ID        int    `json:"id"`
	IssueName string `json:"issue"`
	Number    string `json:"number"`
	CoverDate string `json:"cover_date"`
	StoreDate string `json:"store_date,omitempty"`
	FocDate   string `json:"foc_date,omitempty"`
	Image     string `json:"image,omitempty"`
	CoverHash string `json:"cover_hash,omitempty"`
	Modified  string `json:"modified"`
	Series    struct {
		Name      string `json:"name"`
		Volume    int    `json:"volume"`
		YearBegan int    `json:"year_began"`
	} `json:"series"`
}

// Credit captures one creator + the role(s) they served on an issue.
type Credit struct {
	ID      int           `json:"id"`
	Creator string        `json:"creator"`
	Role    []GenericItem `json:"role"`
}

// Issue is the detail-endpoint payload for /api/issue/{id}/.
type Issue struct {
	ID              int            `json:"id"`
	Number          string         `json:"number"`
	CoverDate       string         `json:"cover_date"`
	StoreDate       string         `json:"store_date,omitempty"`
	FocDate         string         `json:"foc_date,omitempty"`
	Image           string         `json:"image,omitempty"`
	CoverHash       string         `json:"cover_hash,omitempty"`
	Modified        string         `json:"modified"`
	Publisher       GenericItem    `json:"publisher"`
	Imprint         *GenericItem   `json:"imprint"`
	Series          IssueSeries    `json:"series"`
	AltNumber       string         `json:"alt_number,omitempty"`
	CollectionTitle string         `json:"collection_title,omitempty"`
	StoryTitles     []string       `json:"story_titles,omitempty"`
	Price           string         `json:"price,omitempty"`
	PriceCurrency   string         `json:"price_currency,omitempty"`
	Rating          GenericItem    `json:"rating"`
	SKU             string         `json:"sku,omitempty"`
	ISBN            string         `json:"isbn,omitempty"`
	UPC             string         `json:"upc,omitempty"`
	PageCount       *int           `json:"page_count,omitempty"`
	Description     string         `json:"desc,omitempty"`
	Arcs            []BaseResource `json:"arcs,omitempty"`
	Credits         []Credit       `json:"credits,omitempty"`
	Characters      []BaseResource `json:"characters,omitempty"`
	Teams           []BaseResource `json:"teams,omitempty"`
	Universes       []BaseResource `json:"universes,omitempty"`
	CVID            *int           `json:"cv_id"`
	GCDID           *int           `json:"gcd_id"`
}

// QuotaSnapshot is what we know about the rate-limit windows after the most
// recent response. Zero values mean "we haven't observed it yet."
type QuotaSnapshot struct {
	BurstLimit          int
	BurstRemaining      int
	BurstResetUnix      int64
	SustainedLimit      int
	SustainedRemaining  int
	SustainedResetUnix  int64
	LastObservationUnix int64
}

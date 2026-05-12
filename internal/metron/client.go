package metron

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const defaultBaseURL = "https://metron.cloud/api"

// Client is the Metron API client. It is safe for concurrent use; the
// rate limiter serializes outbound requests via its internal mutex.
type Client struct {
	mu       sync.RWMutex
	username string
	token    string
	baseURL  string

	httpClient *http.Client
	limiter    *RateLimiter
}

// NewClient constructs a Metron client. Empty username/token are allowed
// — HasCredentials reports false until SetCredentials is called.
func NewClient(username, token string) *Client {
	return &Client{
		username:   username,
		token:      token,
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		limiter:    NewRateLimiter(),
	}
}

// SetCredentials updates the username + token at runtime. Safe to call
// from a settings handler after the user updates them in the UI.
func (c *Client) SetCredentials(username, token string) {
	c.mu.Lock()
	c.username = strings.TrimSpace(username)
	c.token = strings.TrimSpace(token)
	c.mu.Unlock()
}

// HasCredentials reports whether the client has a non-empty username and
// API token. Callers should gate Metron-touching code paths on this.
func (c *Client) HasCredentials() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.username != "" && c.token != ""
}

// BurstRemaining / DailyRemaining expose the rate-limiter's current
// headroom. Used by the settings endpoint so the UI can show users how
// close they are to the Metron caps.
func (c *Client) BurstRemaining() int { return c.limiter.BurstRemaining() }
func (c *Client) DailyRemaining() int { return c.limiter.DailyRemaining() }

// TestConnection makes the smallest possible authenticated call (one
// series, page 1) and returns nil if the credentials are accepted. Used
// by the settings UI's "Test Connection" button.
func (c *Client) TestConnection() error {
	if !c.HasCredentials() {
		return fmt.Errorf("metron credentials not configured")
	}
	q := url.Values{}
	q.Set("page", "1")
	q.Set("page_size", "1")
	var resp paginated[map[string]any]
	if err := c.get("/series/", q, &resp); err != nil {
		return err
	}
	return nil
}

// SearchSeries queries /series/?name=<query>. Returns the first page of
// results — the calling MetadataService unions this with ComicVine and
// presents both in the search UI.
func (c *Client) SearchSeries(query string) ([]SearchResult, error) {
	if !c.HasCredentials() {
		return nil, fmt.Errorf("metron credentials not configured")
	}
	q := url.Values{}
	q.Set("name", query)
	q.Set("page_size", "25")
	var page paginated[seriesListItem]
	if err := c.get("/series/", q, &page); err != nil {
		return nil, fmt.Errorf("metron series search: %w", err)
	}
	out := make([]SearchResult, 0, len(page.Results))
	for _, r := range page.Results {
		out = append(out, SearchResult{
			ID:          r.ID,
			Name:        stripYearSuffix(r.Name),
			YearStarted: r.YearBegan,
			IssueCount:  r.IssueCount,
			ImageURL:    r.Image,
			Description: r.Description,
		})
	}
	return out, nil
}

// stripYearSuffix removes a trailing " (YYYY)" or " (YYYY-YYYY)" / " (YYYY-)"
// from a Metron series-list display name like "Absolute Batman (2024)" so
// it lines up with the bare names returned by ComicVine's search. Idempotent.
var trailingYearSuffix = regexp.MustCompile(`\s*\((\d{4})(?:-(?:\d{4})?)?\)\s*$`)

func stripYearSuffix(name string) string {
	return strings.TrimSpace(trailingYearSuffix.ReplaceAllString(name, ""))
}

// FindSeriesByCVID resolves a Metron series via its ComicVine cross-reference.
// Metron stores cv_id on each series and exposes it as a query filter, so
// CV-tracked series can be linked without name-heuristic matching.
// Returns nil when Metron has no record for the given CV volume.
func (c *Client) FindSeriesByCVID(cvID int) (*SearchResult, error) {
	if !c.HasCredentials() {
		return nil, fmt.Errorf("metron credentials not configured")
	}
	q := url.Values{}
	q.Set("cv_id", fmt.Sprintf("%d", cvID))
	q.Set("page_size", "5")
	var page paginated[seriesListItem]
	if err := c.get("/series/", q, &page); err != nil {
		return nil, fmt.Errorf("metron find by cv_id: %w", err)
	}
	if len(page.Results) == 0 {
		return nil, nil
	}
	r := page.Results[0]
	return &SearchResult{
		ID:          r.ID,
		Name:        stripYearSuffix(r.Name),
		YearStarted: r.YearBegan,
		IssueCount:  r.IssueCount,
	}, nil
}

// GetSeries fetches /series/{id}/ for full metadata.
func (c *Client) GetSeries(id int) (*Series, error) {
	if !c.HasCredentials() {
		return nil, fmt.Errorf("metron credentials not configured")
	}
	var s Series
	if err := c.get(fmt.Sprintf("/series/%d/", id), nil, &s); err != nil {
		return nil, fmt.Errorf("metron get series %d: %w", id, err)
	}
	return &s, nil
}

// ListIssues fetches every issue under a series. Walks the paginated
// /issue/?series_id=<id> endpoint until exhausted.
func (c *Client) ListIssues(seriesID int) ([]Issue, error) {
	if !c.HasCredentials() {
		return nil, fmt.Errorf("metron credentials not configured")
	}
	var all []Issue
	page := 1
	for {
		q := url.Values{}
		q.Set("series_id", fmt.Sprintf("%d", seriesID))
		q.Set("page", fmt.Sprintf("%d", page))
		q.Set("page_size", "100")
		var p paginated[issueListItem]
		if err := c.get("/issue/", q, &p); err != nil {
			return nil, fmt.Errorf("metron list issues page %d: %w", page, err)
		}
		for _, r := range p.Results {
			all = append(all, Issue{
				ID:        r.ID,
				SeriesID:  seriesID,
				Number:    r.Number,
				StoreDate: r.StoreDate,
				CoverDate: r.CoverDate,
				ImageURL:  r.Image,
			})
		}
		if p.Next == nil || *p.Next == "" {
			break
		}
		page++
		if page > 50 { // safety stop
			break
		}
	}
	return all, nil
}

// get performs an authenticated GET with rate-limiting and decodes the
// response into `into`.
func (c *Client) get(path string, query url.Values, into any) error {
	c.limiter.Wait()

	c.mu.RLock()
	baseURL := c.baseURL
	auth := basicAuth(c.username, c.token)
	c.mu.RUnlock()

	reqURL := baseURL + path
	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Basic "+auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "LongBox/1.0 (Comic Library Manager)")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("metron auth rejected (401) — check username/token")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("metron rate-limited (429)")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("metron status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("decoding metron response: %w", err)
	}
	slog.Debug("metron call", "path", path)
	return nil
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// Wire-format helpers — Metron returns slightly different shapes for list
// endpoints vs detail endpoints. These mirror only the fields we use.

type seriesListItem struct {
	ID          int    `json:"id"`
	Name        string `json:"series"` // list endpoint flattens series.name → "series"
	YearBegan   int    `json:"year_began"`
	IssueCount  int    `json:"issue_count"`
	Description string `json:"desc"`
	Image       string `json:"image"`
}

type issueListItem struct {
	ID        int    `json:"id"`
	Number    string `json:"number"`
	StoreDate string `json:"store_date"`
	CoverDate string `json:"cover_date"`
	Image     string `json:"image"`
}

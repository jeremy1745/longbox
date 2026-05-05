package metron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const baseURL = "https://metron.cloud/api"

// Client provides access to the Metron Comic Database REST API.
type Client struct {
	mu       sync.Mutex
	username string
	apiToken string

	httpClient *http.Client
	limiter    *rateLimiter
}

// NewClient constructs a client. Credentials are optional at construction
// time — set them later via SetCredentials. All API calls fail fast with
// ErrNoCredentials when unset.
func NewClient(username, apiToken string) *Client {
	return &Client{
		username: username,
		apiToken: apiToken,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		limiter: newRateLimiter(),
	}
}

// ErrNoCredentials is returned when an API method is called without a
// configured username + token pair.
var ErrNoCredentials = errors.New("metron credentials not configured")

// ErrNotFound is returned when the server responds with 404. Useful for
// distinguishing "not in Metron" from real errors.
var ErrNotFound = errors.New("metron: resource not found")

// ErrNotModified is returned when an If-Modified-Since request hits a 304.
// Per Metron's docs this *does not* count against the rate-limit quota.
var ErrNotModified = errors.New("metron: not modified")

// SetCredentials updates the username and API token. Safe to call from any
// goroutine.
func (c *Client) SetCredentials(username, apiToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.username = username
	c.apiToken = apiToken
}

// HasCredentials reports whether credentials have been configured.
func (c *Client) HasCredentials() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.username != "" && c.apiToken != ""
}

// Quota returns the most recent QuotaSnapshot — useful for surfacing
// remaining-request counts in the UI.
func (c *Client) Quota() QuotaSnapshot {
	return c.limiter.snapshot()
}

// get performs an authenticated, rate-limited GET with automatic retry on
// 429 (per Metron best-practices: 429 is the only 4xx worth retrying).
// Honors the server's Retry-After header for the wait duration. Up to 3
// total attempts.
func (c *Client) get(ctx context.Context, endpoint string, params url.Values, ifModifiedSince time.Time) ([]byte, *http.Response, error) {
	const maxAttempts = 3
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		body, resp, err := c.doGet(ctx, endpoint, params, ifModifiedSince)
		if err == nil {
			return body, resp, nil
		}
		lastErr = err
		if resp != nil && resp.StatusCode == http.StatusTooManyRequests {
			// observeRetryAfter already set pauseUntil; the next wait()
			// inside doGet will honor it. Loop and retry.
			slog.Warn("metron 429 — retrying after server Retry-After",
				"attempt", attempt+1, "endpoint", endpoint)
			continue
		}
		// Any other error: surface immediately, no retry.
		return body, resp, err
	}
	return nil, nil, fmt.Errorf("metron: exhausted retries: %w", lastErr)
}

// doGet is the single-attempt request. get wraps this with retry on 429.
func (c *Client) doGet(ctx context.Context, endpoint string, params url.Values, ifModifiedSince time.Time) ([]byte, *http.Response, error) {
	c.mu.Lock()
	username := c.username
	token := c.apiToken
	c.mu.Unlock()
	if username == "" || token == "" {
		return nil, nil, ErrNoCredentials
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if err := c.limiter.wait(ctx); err != nil {
		return nil, nil, err
	}

	reqURL := endpoint
	if !strings.HasPrefix(reqURL, "http") {
		reqURL = strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(endpoint, "/")
	}
	if params != nil {
		if strings.Contains(reqURL, "?") {
			reqURL += "&" + params.Encode()
		} else {
			reqURL += "?" + params.Encode()
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("creating request: %w", err)
	}
	req.SetBasicAuth(username, token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "LongBox/1.0 (Comic Library Manager)")
	if !ifModifiedSince.IsZero() {
		req.Header.Set("If-Modified-Since", ifModifiedSince.UTC().Format(http.TimeFormat))
	}

	// Path is logged but the Authorization header is intentionally never
	// included in any log output (Metron docs flag this explicitly).
	slog.Debug("metron api request", "endpoint", endpoint)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("executing request: %w", err)
	}

	c.limiter.observe(resp.Header)

	switch resp.StatusCode {
	case http.StatusNotModified:
		resp.Body.Close()
		return nil, resp, ErrNotModified
	case http.StatusNotFound:
		resp.Body.Close()
		return nil, resp, ErrNotFound
	case http.StatusTooManyRequests:
		c.limiter.observeRetryAfter(resp.Header)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, resp, fmt.Errorf("metron: 429 rate limited: %s", strings.TrimSpace(string(body)))
	}

	defer resp.Body.Close()
	if resp.StatusCode/100 == 4 {
		body, _ := io.ReadAll(resp.Body)
		return nil, resp, fmt.Errorf("metron: HTTP %d (no retry): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, resp, fmt.Errorf("metron: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp, fmt.Errorf("reading response: %w", err)
	}
	return body, resp, nil
}

// SearchSeries lists series matching the given filters. Common params:
// "name" (case-insensitive contains), "publisher_id", "year_began", "cv_id",
// "gcd_id". Walks one page; caller paginates via the returned Next URL.
func (c *Client) SearchSeries(ctx context.Context, params url.Values) (*ListResponse[SeriesListItem], error) {
	body, _, err := c.get(ctx, "/series/", params, time.Time{})
	if err != nil {
		return nil, err
	}
	var resp ListResponse[SeriesListItem]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing series list: %w", err)
	}
	return &resp, nil
}

// SeriesByCVID is a convenience for the common "I have a ComicVine ID, give
// me the matching Metron series" cross-reference. Returns nil if Metron has
// no series with that cv_id.
func (c *Client) SeriesByCVID(ctx context.Context, cvID int) (*SeriesListItem, error) {
	params := url.Values{}
	params.Set("cv_id", fmt.Sprintf("%d", cvID))
	resp, err := c.SearchSeries(ctx, params)
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Results) == 0 {
		return nil, nil
	}
	r := resp.Results[0]
	return &r, nil
}

// GetSeries fetches /api/series/{id}/.
func (c *Client) GetSeries(ctx context.Context, id int) (*Series, error) {
	s, _, err := c.GetSeriesIfModified(ctx, id, time.Time{})
	return s, err
}

// GetSeriesIfModified is the conditional-GET variant of GetSeries. When
// ifModifiedSince is non-zero it's sent as the If-Modified-Since header; a
// 304 response returns (nil, "", ErrNotModified) and does NOT count against
// the rate-limit quota. On 200 the second return is the server's
// Last-Modified header value, suitable for storing and feeding back in next
// time.
func (c *Client) GetSeriesIfModified(ctx context.Context, id int, ifModifiedSince time.Time) (*Series, string, error) {
	body, resp, err := c.get(ctx, fmt.Sprintf("/series/%d/", id), nil, ifModifiedSince)
	if err != nil {
		return nil, "", err
	}
	var s Series
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, "", fmt.Errorf("parsing series: %w", err)
	}
	lastMod := ""
	if resp != nil {
		lastMod = resp.Header.Get("Last-Modified")
	}
	return &s, lastMod, nil
}

// GetSeriesIssues paginates /api/issue/?series_id={id}, returning every
// issue. Walks Next URLs sequentially as required by the rate-limit policy.
func (c *Client) GetSeriesIssues(ctx context.Context, seriesID int) ([]IssueListItem, error) {
	params := url.Values{}
	params.Set("series_id", fmt.Sprintf("%d", seriesID))
	params.Set("ordering", "cover_date")
	return c.paginateIssues(ctx, "/issue/", params)
}

// SearchIssues lists issues matching the given filters. Useful filters:
// "series_name", "series_id", "number", "series_year_began",
// "store_date_range_after", "store_date_range_before", "cv_id".
func (c *Client) SearchIssues(ctx context.Context, params url.Values) (*ListResponse[IssueListItem], error) {
	body, _, err := c.get(ctx, "/issue/", params, time.Time{})
	if err != nil {
		return nil, err
	}
	var resp ListResponse[IssueListItem]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing issue list: %w", err)
	}
	return &resp, nil
}

// GetIssue fetches /api/issue/{id}/.
func (c *Client) GetIssue(ctx context.Context, id int) (*Issue, error) {
	body, _, err := c.get(ctx, fmt.Sprintf("/issue/%d/", id), nil, time.Time{})
	if err != nil {
		return nil, err
	}
	var i Issue
	if err := json.Unmarshal(body, &i); err != nil {
		return nil, fmt.Errorf("parsing issue: %w", err)
	}
	return &i, nil
}

// IssueByCVID returns the Metron issue cross-referenced to a given
// ComicVine issue ID, or nil if not present.
func (c *Client) IssueByCVID(ctx context.Context, cvID int) (*IssueListItem, error) {
	params := url.Values{}
	params.Set("cv_id", fmt.Sprintf("%d", cvID))
	resp, err := c.SearchIssues(ctx, params)
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Results) == 0 {
		return nil, nil
	}
	r := resp.Results[0]
	return &r, nil
}

// paginateIssues walks every page of an issue list, following the server's
// `next` URLs. Sequential per the API best-practices guidance.
func (c *Client) paginateIssues(ctx context.Context, endpoint string, params url.Values) ([]IssueListItem, error) {
	var all []IssueListItem
	first := true
	cursor := endpoint
	for {
		var (
			body []byte
			err  error
		)
		if first {
			body, _, err = c.get(ctx, cursor, params, time.Time{})
		} else {
			body, _, err = c.get(ctx, cursor, nil, time.Time{})
		}
		if err != nil {
			return all, err
		}
		var page ListResponse[IssueListItem]
		if err := json.Unmarshal(body, &page); err != nil {
			return all, fmt.Errorf("parsing issue page: %w", err)
		}
		all = append(all, page.Results...)
		if page.Next == "" {
			return all, nil
		}
		cursor = page.Next
		first = false
		// Safety cap — Metron paginates 28 per page; 200 pages = 5600 issues.
		if len(all) > 20000 {
			slog.Warn("metron pagination cap hit", "endpoint", endpoint, "count", len(all))
			return all, nil
		}
	}
}

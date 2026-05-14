package prowlarr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// defaultCategory is the Newznab category for Books > Comics.
const defaultCategory = "7030"

// Client is a REST client for the Prowlarr indexer-manager API.
// It handles auth, rate limiting, and forwarding grab requests to Prowlarr's
// configured download client.
type Client struct {
	baseURL  string // e.g. "http://192.168.1.x:9696" — no trailing slash, no /api path
	apiKey   string
	category string // Newznab category id, default "7030" (Books > Comics)
	http     *http.Client
	limiter  *rateLimiter
}

// Release is a single search result from Prowlarr. Only fields relevant to
// LongBox's acquisition flow are included — YAGNI.
type Release struct {
	GUID        string    `json:"guid"`
	IndexerID   int       `json:"indexerId"`
	Indexer     string    `json:"indexer"`
	Title       string    `json:"title"`
	Size        int64     `json:"size"`
	PublishDate time.Time `json:"publishDate"` // ISO-8601; encoding/json parses RFC3339 into time.Time automatically
	DownloadURL string    `json:"downloadUrl"`
	Protocol    string    `json:"protocol"` // "usenet" | "torrent"
}

// NewClient constructs a Prowlarr client. baseURL should be the host+port with
// no trailing slash and no /api path (e.g. "http://192.168.1.100:9696").
// If category is empty it defaults to "7030" (Books > Comics).
func NewClient(baseURL, apiKey, category string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	if category == "" {
		category = defaultCategory
	}
	return &Client{
		baseURL:  baseURL,
		apiKey:   apiKey,
		category: category,
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
		limiter: newRateLimiter(),
	}
}

// SearchIssue queries Prowlarr for releases matching a comic issue. year is
// accepted so callers can use it for scoring, but it is NOT sent in the query
// string — indexer titles rarely include the year, so including it tends to
// hurt recall more than it helps precision.
func (c *Client) SearchIssue(ctx context.Context, series, issueNumber string, year int) ([]Release, error) {
	query := fmt.Sprintf("%s %s", series, issueNumber)

	if err := c.limiter.wait(ctx); err != nil {
		return nil, err
	}

	reqURL := fmt.Sprintf("%s/api/v1/search?query=%s&type=search&categories=%s",
		c.baseURL,
		urlEncode(query),
		urlEncode(c.category),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("prowlarr: creating search request: %w", err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prowlarr: search request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return nil, fmt.Errorf("prowlarr: search HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("prowlarr: decoding search response: %w", err)
	}
	// Zero results is not an error.
	if releases == nil {
		releases = []Release{}
	}
	return releases, nil
}

// GrabRelease tells Prowlarr to grab the identified release and forward it to
// its configured download client (SABnzbd, qBittorrent, etc.). Prowlarr
// handles the forwarding automatically — callers just supply the guid and
// indexerId from a prior SearchIssue result.
func (c *Client) GrabRelease(ctx context.Context, guid string, indexerID int) error {
	if err := c.limiter.wait(ctx); err != nil {
		return err
	}

	body, err := json.Marshal(struct {
		GUID      string `json:"guid"`
		IndexerID int    `json:"indexerId"`
	}{GUID: guid, IndexerID: indexerID})
	if err != nil {
		return fmt.Errorf("prowlarr: marshalling grab request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("%s/api/v1/search", c.baseURL),
		bytes.NewReader(body),
	)
	if err != nil {
		return fmt.Errorf("prowlarr: creating grab request: %w", err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("prowlarr: grab request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("prowlarr: grab HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// TestConnection checks whether Prowlarr is reachable and the API key is valid
// by calling the lightweight system/status endpoint. Used by the settings
// "Test" button.
func (c *Client) TestConnection(ctx context.Context) error {
	if err := c.limiter.wait(ctx); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/v1/system/status", c.baseURL),
		nil,
	)
	if err != nil {
		return fmt.Errorf("prowlarr: creating status request: %w", err)
	}
	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("prowlarr: status request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("prowlarr: status HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	return nil
}

// urlEncode percent-encodes a string for use in a query parameter value.
func urlEncode(s string) string {
	// net/url.QueryEscape encodes spaces as "+"; use PathEscape for %20.
	// Either works for Prowlarr but %20 is more universally correct.
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '~':
			b.WriteRune(r)
		default:
			encoded := fmt.Sprintf("%%%02X", r)
			b.WriteString(encoded)
		}
	}
	return b.String()
}

package newznab

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to a single Newznab-compatible indexer.
type Client struct {
	baseURL    string
	apiKey     string
	isProwlarr bool
	httpClient *http.Client
	limiter    *RateLimiter
}

// NewClient creates a Newznab client for the given indexer.
// If isProwlarr is true, the API key is sent via X-Api-Key header instead of query param.
func NewClient(baseURL, apiKey string, isProwlarr bool) *Client {
	u := strings.TrimRight(baseURL, "/")
	if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "https://" + u
	}
	timeout := 30 * time.Second
	if isProwlarr {
		timeout = 120 * time.Second // Prowlarr fans out to multiple indexers
	}
	return &Client{
		baseURL:    u,
		apiKey:     apiKey,
		isProwlarr: isProwlarr,
		httpClient: &http.Client{Timeout: timeout},
		limiter:    NewRateLimiter(5),
	}
}

// Search queries the indexer for NZBs matching the query within the given categories.
//
// Background-context shim — for callers that don't have a request context.
// New code should call SearchCtx so cancellation propagates to the HTTP
// request and a slow indexer doesn't pin a request handler.
func (c *Client) Search(query string, categories []string) ([]SearchResult, error) {
	return c.SearchCtx(context.Background(), query, categories)
}

// SearchCtx is the ctx-aware variant. Cancellation aborts the in-flight
// indexer request instead of waiting for the HTTP timeout.
func (c *Client) SearchCtx(ctx context.Context, query string, categories []string) ([]SearchResult, error) {
	c.limiter.Wait()

	if c.isProwlarr {
		return c.searchProwlarr(ctx, query, categories)
	}

	params := url.Values{}
	params.Set("t", "search")
	params.Set("q", query)
	params.Set("limit", "100")
	if len(categories) > 0 {
		params.Set("cat", strings.Join(categories, ","))
	}

	body, err := c.doRequestCtx(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("search request: %w", err)
	}

	var resp Response
	if err := xml.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing search response: %w", err)
	}

	results := make([]SearchResult, 0, len(resp.Channel.Items))
	for _, item := range resp.Channel.Items {
		result := SearchResult{
			Title:    item.Title,
			NZBURL:   item.Link,
			GUID:     item.GUID.Value,
			Category: item.Category,
			Size:     item.Size,
		}

		// Parse pub date
		if item.PubDate != "" {
			if t, err := time.Parse(time.RFC1123Z, item.PubDate); err == nil {
				result.PublishDate = t
			} else if t, err := time.Parse(time.RFC1123, item.PubDate); err == nil {
				result.PublishDate = t
			}
		}

		// Extract size and grabs from Newznab attributes if not in the main fields
		for _, attr := range item.Attributes {
			switch attr.Name {
			case "size":
				if result.Size == 0 {
					result.Size, _ = strconv.ParseInt(attr.Value, 10, 64)
				}
			case "grabs":
				result.Grabs, _ = strconv.Atoi(attr.Value)
			}
		}

		// Use link as NZB URL; if empty, construct from GUID.
		// Don't include the API key — it gets reattached at grab time.
		if result.NZBURL == "" && result.GUID != "" {
			result.NZBURL = fmt.Sprintf("%s/api?t=get&id=%s", c.baseURL, result.GUID)
		}

		results = append(results, result)
	}

	slog.Debug("newznab search complete",
		"query", query,
		"results", len(results),
		"total", resp.Channel.Response.Total,
	)

	return results, nil
}

// searchProwlarr uses Prowlarr's native JSON API to search across all its indexers.
//
// Categories are intentionally NOT forwarded to Prowlarr. Prowlarr translates
// Newznab category IDs per-indexer, and many downstream Usenet indexers don't
// classify comics under 7030 (Books > Comics) — they use 8000 (Other) or
// nothing at all. Forcing a category through Prowlarr's `/api/v1/search`
// causes those indexers to return zero hits even when the same release shows
// up in Prowlarr's manual UI. Letting Prowlarr search uncategorized reproduces
// the manual-UI behavior.
func (c *Client) searchProwlarr(ctx context.Context, query string, categories []string) ([]SearchResult, error) {
	_ = categories // see comment above
	params := url.Values{}
	params.Set("query", query)
	params.Set("type", "search")
	params.Set("limit", "100")

	reqURL := fmt.Sprintf("%s/api/v1/search?%s", c.baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating prowlarr request: %w", err)
	}
	req.Header.Set("User-Agent", "LongBox/1.0 (Comic Library Manager)")
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("prowlarr search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("prowlarr returned status %d: %s", resp.StatusCode, string(body))
	}

	var prowlarrResults []prowlarrSearchResult
	if err := json.NewDecoder(resp.Body).Decode(&prowlarrResults); err != nil {
		return nil, fmt.Errorf("parsing prowlarr response: %w", err)
	}

	results := make([]SearchResult, 0, len(prowlarrResults))
	for _, pr := range prowlarrResults {
		result := SearchResult{
			Title:   pr.Title,
			NZBURL:  pr.DownloadURL,
			GUID:    pr.GUID,
			Size:    pr.Size,
			Grabs:   pr.Grabs,
			InfoURL: pr.InfoURL,
		}
		if pr.PublishDate != "" {
			if t, err := time.Parse(time.RFC3339, pr.PublishDate); err == nil {
				result.PublishDate = t
			}
		}
		if len(pr.Categories) > 0 {
			result.Category = pr.Categories[0].Name
		}
		results = append(results, result)
	}

	slog.Debug("prowlarr search complete",
		"query", query,
		"results", len(results),
	)

	return results, nil
}

// TestConnection verifies API access by calling the caps endpoint.
// For Prowlarr, we do a lightweight search instead since it doesn't
// support the standard Newznab caps endpoint.
func (c *Client) TestConnection() error {
	c.limiter.Wait()

	if c.isProwlarr {
		return c.testProwlarr()
	}

	params := url.Values{}
	params.Set("t", "caps")

	body, err := c.doRequest(params)
	if err != nil {
		return fmt.Errorf("caps request: %w", err)
	}

	if len(body) == 0 {
		return fmt.Errorf("indexer returned empty response")
	}

	var caps CapsResponse
	if err := xml.Unmarshal(body, &caps); err != nil {
		return fmt.Errorf("parsing caps response: %w", err)
	}

	slog.Info("newznab connection test passed", "server", caps.Server.Title)
	return nil
}

// testProwlarr verifies Prowlarr connectivity by hitting its health API.
func (c *Client) testProwlarr() error {
	reqURL := fmt.Sprintf("%s/api/v1/health", c.baseURL)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "LongBox/1.0 (Comic Library Manager)")
	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("authentication failed (HTTP %d) — check your API key", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Prowlarr returned status %d: %s", resp.StatusCode, string(body))
	}

	slog.Info("prowlarr connection test passed")
	return nil
}

func (c *Client) doRequest(params url.Values) ([]byte, error) {
	return c.doRequestCtx(context.Background(), params)
}

func (c *Client) doRequestCtx(ctx context.Context, params url.Values) ([]byte, error) {
	if !c.isProwlarr {
		params.Set("apikey", c.apiKey)
	}

	reqURL := fmt.Sprintf("%s/api?%s", c.baseURL, params.Encode())

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "LongBox/1.0 (Comic Library Manager)")

	if c.isProwlarr {
		req.Header.Set("X-Api-Key", c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("indexer returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	return body, nil
}

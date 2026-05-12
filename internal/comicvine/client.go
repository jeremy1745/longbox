package comicvine

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const baseURL = "https://comicvine.gamespot.com/api"

// Client provides access to the ComicVine API.
type Client struct {
	apiKey     string
	httpClient *http.Client
	limiter    *RateLimiter
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		limiter: NewRateLimiter(),
	}
}

// SetAPIKey updates the API key (used when loading from settings).
func (c *Client) SetAPIKey(key string) {
	c.apiKey = key
}

// HasAPIKey returns true if an API key is configured.
func (c *Client) HasAPIKey() bool {
	return c.apiKey != ""
}

// HourlyRemaining returns how many API requests are left this hour.
func (c *Client) HourlyRemaining() int {
	return c.limiter.HourlyRemaining()
}

// get performs a rate-limited GET request to the ComicVine API.
func (c *Client) get(endpoint string, params url.Values) ([]byte, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("ComicVine API key not configured")
	}

	c.limiter.Wait()

	if params == nil {
		params = url.Values{}
	}
	params.Set("api_key", c.apiKey)
	params.Set("format", "json")

	reqURL := fmt.Sprintf("%s/%s?%s", baseURL, endpoint, params.Encode())

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "LongBox/1.0 (Comic Library Manager)")

	slog.Debug("comicvine api request", "endpoint", endpoint)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	return body, nil
}

// SearchVolumes searches for comic volumes (series) by name.
func (c *Client) SearchVolumes(query string, page int) ([]SearchResult, int, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("resources", "volume")
	params.Set("limit", "10")
	params.Set("offset", fmt.Sprintf("%d", (page-1)*10))
	params.Set("field_list", "id,name,start_year,count_of_issues,description,publisher,image,resource_type")

	body, err := c.get("search", params)
	if err != nil {
		return nil, 0, err
	}

	var resp APIResponse[[]SearchResult]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, fmt.Errorf("parsing response: %w", err)
	}

	if resp.StatusCode != 1 {
		return nil, 0, fmt.Errorf("API error: %s (code %d)", resp.Error, resp.StatusCode)
	}

	return resp.Results, resp.NumberOfTotalResults, nil
}

// GetVolume fetches detailed volume (series) info including issue list.
func (c *Client) GetVolume(cvID int) (*Volume, error) {
	params := url.Values{}
	params.Set("field_list", "id,name,start_year,description,count_of_issues,publisher,image,site_detail_url,issues")

	body, err := c.get(fmt.Sprintf("volume/4050-%d", cvID), params)
	if err != nil {
		return nil, err
	}

	var resp APIResponse[Volume]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if resp.StatusCode != 1 {
		return nil, fmt.Errorf("API error: %s (code %d)", resp.Error, resp.StatusCode)
	}

	return &resp.Results, nil
}

// GetIssue fetches detailed issue info.
func (c *Client) GetIssue(cvID int) (*Issue, error) {
	params := url.Values{}
	params.Set("field_list", "id,name,issue_number,description,cover_date,store_date,image,site_detail_url,volume,person_credits")

	body, err := c.get(fmt.Sprintf("issue/4000-%d", cvID), params)
	if err != nil {
		return nil, err
	}

	var resp APIResponse[Issue]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if resp.StatusCode != 1 {
		return nil, fmt.Errorf("API error: %s (code %d)", resp.Error, resp.StatusCode)
	}

	return &resp.Results, nil
}

// GetVolumeIssues fetches all issues for a volume, handling pagination.
func (c *Client) GetVolumeIssues(volumeID int) ([]Issue, error) {
	var allIssues []Issue
	offset := 0
	limit := 100

	for {
		params := url.Values{}
		params.Set("filter", fmt.Sprintf("volume:%d", volumeID))
		params.Set("field_list", "id,name,issue_number,description,cover_date,store_date,image,volume,person_credits")
		params.Set("sort", "issue_number:asc")
		params.Set("limit", fmt.Sprintf("%d", limit))
		params.Set("offset", fmt.Sprintf("%d", offset))

		body, err := c.get("issues", params)
		if err != nil {
			return nil, err
		}

		var resp APIResponse[[]Issue]
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parsing response: %w", err)
		}

		if resp.StatusCode != 1 {
			return nil, fmt.Errorf("API error: %s (code %d)", resp.Error, resp.StatusCode)
		}

		allIssues = append(allIssues, resp.Results...)

		if len(resp.Results) < limit {
			break
		}
		offset += limit
	}

	return allIssues, nil
}

// SearchStoryArcs searches for story arcs by name.
func (c *Client) SearchStoryArcs(query string, page int) ([]SearchResult, int, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("resources", "story_arc")
	params.Set("limit", "10")
	params.Set("offset", fmt.Sprintf("%d", (page-1)*10))
	params.Set("field_list", "id,name,description,image,resource_type")

	body, err := c.get("search", params)
	if err != nil {
		return nil, 0, err
	}

	var resp APIResponse[[]SearchResult]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, 0, fmt.Errorf("parsing response: %w", err)
	}

	if resp.StatusCode != 1 {
		return nil, 0, fmt.Errorf("API error: %s (code %d)", resp.Error, resp.StatusCode)
	}

	return resp.Results, resp.NumberOfTotalResults, nil
}

// GetStoryArc fetches a story arc by ComicVine ID.
func (c *Client) GetStoryArc(cvID int) (*StoryArc, error) {
	params := url.Values{}
	params.Set("field_list", "id,name,description,image,issues")

	body, err := c.get(fmt.Sprintf("story_arc/4045-%d", cvID), params)
	if err != nil {
		return nil, err
	}

	var resp APIResponse[StoryArc]
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	if resp.StatusCode != 1 {
		return nil, fmt.Errorf("API error: %s (code %d)", resp.Error, resp.StatusCode)
	}

	return &resp.Results, nil
}

// GetIssuesByStoreDate fetches all issues from ComicVine with store_date in the given range.
// Dates should be in YYYY-MM-DD format. This returns ALL comics releasing in that window.
func (c *Client) GetIssuesByStoreDate(startDate, endDate string) ([]Issue, error) {
	var allIssues []Issue
	offset := 0
	limit := 100

	for {
		params := url.Values{}
		params.Set("filter", fmt.Sprintf("store_date:%s|%s", startDate, endDate))
		params.Set("field_list", "id,name,issue_number,description,cover_date,store_date,image,volume,person_credits")
		params.Set("sort", "store_date:asc")
		params.Set("limit", fmt.Sprintf("%d", limit))
		params.Set("offset", fmt.Sprintf("%d", offset))

		body, err := c.get("issues", params)
		if err != nil {
			return nil, err
		}

		var resp APIResponse[[]Issue]
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, fmt.Errorf("parsing response: %w", err)
		}

		if resp.StatusCode != 1 {
			return nil, fmt.Errorf("API error: %s (code %d)", resp.Error, resp.StatusCode)
		}

		allIssues = append(allIssues, resp.Results...)

		slog.Debug("fetched issues by store date",
			"offset", offset,
			"page_results", len(resp.Results),
			"total_results", resp.NumberOfTotalResults,
		)

		if len(resp.Results) < limit {
			break
		}
		offset += limit

		// Safety: cap at 1000 issues per query to avoid runaway pagination
		if offset >= 1000 {
			slog.Warn("store date query hit 1000 issue cap", "start", startDate, "end", endDate)
			break
		}
	}

	return allIssues, nil
}


// GetVolumesByIDs fetches multiple volumes by their ComicVine IDs.
// This is used to look up publisher names for issues fetched by store_date.
// ComicVine supports filtering volumes by pipe-separated IDs.
func (c *Client) GetVolumesByIDs(ids []int) ([]Volume, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	var allVolumes []Volume

	// Process in batches of 100 (ComicVine filter limit)
	batchSize := 100
	for i := 0; i < len(ids); i += batchSize {
		end := i + batchSize
		if end > len(ids) {
			end = len(ids)
		}
		batch := ids[i:end]

		// Build pipe-separated ID filter
		idStrs := make([]string, len(batch))
		for j, id := range batch {
			idStrs[j] = fmt.Sprintf("%d", id)
		}
		idFilter := strings.Join(idStrs, "|")

		params := url.Values{}
		params.Set("filter", fmt.Sprintf("id:%s", idFilter))
		params.Set("field_list", "id,name,publisher")
		params.Set("limit", fmt.Sprintf("%d", batchSize))

		body, err := c.get("volumes", params)
		if err != nil {
			return allVolumes, fmt.Errorf("fetching volumes batch: %w", err)
		}

		var resp APIResponse[[]Volume]
		if err := json.Unmarshal(body, &resp); err != nil {
			return allVolumes, fmt.Errorf("parsing volumes response: %w", err)
		}

		if resp.StatusCode != 1 {
			return allVolumes, fmt.Errorf("API error: %s (code %d)", resp.Error, resp.StatusCode)
		}

		allVolumes = append(allVolumes, resp.Results...)

		slog.Debug("fetched volumes batch for publisher lookup",
			"batch_start", i,
			"batch_size", len(batch),
			"results", len(resp.Results),
		)
	}

	return allVolumes, nil
}

// StripHTML removes HTML tags from ComicVine descriptions.
func StripHTML(s string) string {
	// Simple HTML stripping - handles the common ComicVine description format
	var result strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			result.WriteRune(r)
		}
	}
	return strings.TrimSpace(result.String())
}

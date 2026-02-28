package sabnzbd

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

// Client talks to a SABnzbd instance.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a SABnzbd API client.
func NewClient(baseURL, apiKey string) *Client {
	u := strings.TrimRight(baseURL, "/")
	if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "http://" + u
	}
	return &Client{
		baseURL:    u,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// QueueSlot represents a single item in the SABnzbd download queue.
type QueueSlot struct {
	NZOID      string  `json:"nzo_id"`
	Filename   string  `json:"filename"`
	Status     string  `json:"status"`
	Percentage string  `json:"percentage"`
	Size       string  `json:"size"`
	TimeLeft   string  `json:"timeleft"`
	Category   string  `json:"cat"`
}

// HistorySlot represents a completed download in SABnzbd history.
type HistorySlot struct {
	NZOID       string `json:"nzo_id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Storage     string `json:"storage"`
	Bytes       int64  `json:"bytes"`
	Category    string `json:"category"`
	FailMessage string `json:"fail_message"`
}

// SendURL sends an NZB URL to SABnzbd for download.
// Returns the nzo_id of the queued item.
func (c *Client) SendURL(nzbURL, title, category string) (string, error) {
	params := url.Values{}
	params.Set("mode", "addurl")
	params.Set("name", nzbURL)
	params.Set("nzbname", title)
	params.Set("cat", category)

	var resp struct {
		Status bool   `json:"status"`
		NZOIDS []string `json:"nzo_ids"`
		Error  string `json:"error"`
	}

	if err := c.doRequest(params, &resp); err != nil {
		return "", fmt.Errorf("sending NZB URL: %w", err)
	}
	if !resp.Status {
		return "", fmt.Errorf("SABnzbd rejected NZB: %s", resp.Error)
	}
	if len(resp.NZOIDS) == 0 {
		return "", fmt.Errorf("SABnzbd returned no nzo_id")
	}

	nzoID := resp.NZOIDS[0]
	slog.Info("NZB sent to SABnzbd", "nzo_id", nzoID, "title", title)
	return nzoID, nil
}

// GetQueue returns the current download queue.
func (c *Client) GetQueue() ([]QueueSlot, error) {
	params := url.Values{}
	params.Set("mode", "queue")

	var resp struct {
		Queue struct {
			Slots []QueueSlot `json:"slots"`
		} `json:"queue"`
	}

	if err := c.doRequest(params, &resp); err != nil {
		return nil, fmt.Errorf("getting queue: %w", err)
	}

	return resp.Queue.Slots, nil
}

// GetHistory returns recent download history.
func (c *Client) GetHistory(limit int) ([]HistorySlot, error) {
	params := url.Values{}
	params.Set("mode", "history")
	params.Set("limit", fmt.Sprintf("%d", limit))

	var resp struct {
		History struct {
			Slots []HistorySlot `json:"slots"`
		} `json:"history"`
	}

	if err := c.doRequest(params, &resp); err != nil {
		return nil, fmt.Errorf("getting history: %w", err)
	}

	return resp.History.Slots, nil
}

// GetSlotStatus returns the status of a specific download by nzo_id.
// Returns the status string ("Downloading", "Queued", "Completed", "Failed", etc.)
// and whether it was found at all.
func (c *Client) GetSlotStatus(nzoID string) (string, bool, error) {
	// Check queue first
	queue, err := c.GetQueue()
	if err != nil {
		return "", false, err
	}
	for _, slot := range queue {
		if slot.NZOID == nzoID {
			return slot.Status, true, nil
		}
	}

	// Check history
	history, err := c.GetHistory(100)
	if err != nil {
		return "", false, err
	}
	for _, slot := range history {
		if slot.NZOID == nzoID {
			return slot.Status, true, nil
		}
	}

	return "", false, nil
}

// TestConnection verifies connectivity by calling the version endpoint.
// Returns the SABnzbd version string.
func (c *Client) TestConnection() (string, error) {
	params := url.Values{}
	params.Set("mode", "version")

	var resp struct {
		Version string `json:"version"`
	}

	if err := c.doRequest(params, &resp); err != nil {
		return "", fmt.Errorf("testing connection: %w", err)
	}

	slog.Info("SABnzbd connection test passed", "version", resp.Version)
	return resp.Version, nil
}

func (c *Client) doRequest(params url.Values, result interface{}) error {
	params.Set("apikey", c.apiKey)
	params.Set("output", "json")

	reqURL := fmt.Sprintf("%s/api?%s", c.baseURL, params.Encode())

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "LongBox/1.0 (Comic Library Manager)")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("SABnzbd returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if err := json.Unmarshal(body, result); err != nil {
		return fmt.Errorf("parsing response: %w", err)
	}

	return nil
}

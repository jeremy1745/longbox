package walksoftly

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

const baseURL = "https://walksoftly.itsaninja.party/newcomics.php"

// Client provides access to the walksoftly weekly release service.
type Client struct {
	httpClient *http.Client
	mu         sync.Mutex
	lastCall   time.Time
}

// NewClient creates a new walksoftly API client.
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetWeeklyReleases fetches all comics releasing in the given week.
// Background-context shim — new code should use GetWeeklyReleasesCtx.
func (c *Client) GetWeeklyReleases(weekNum int, year int) ([]Release, error) {
	return c.GetWeeklyReleasesCtx(context.Background(), weekNum, year)
}

// GetWeeklyReleasesCtx is the ctx-aware variant. Cancellation aborts the
// in-flight HTTP request so the caller's request handler isn't pinned to a
// stalled walksoftly server.
func (c *Client) GetWeeklyReleasesCtx(ctx context.Context, weekNum int, year int) ([]Release, error) {
	// Simple rate limit: 1 request per second. ctx-aware so a cancelled
	// caller doesn't block on the per-second pacing wait.
	c.mu.Lock()
	elapsed := time.Since(c.lastCall)
	if elapsed < time.Second {
		t := time.NewTimer(time.Second - elapsed)
		c.mu.Unlock()
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return nil, ctx.Err()
		}
	} else {
		c.mu.Unlock()
	}
	c.mu.Lock()
	c.lastCall = time.Now()
	c.mu.Unlock()

	reqURL := fmt.Sprintf("%s?week=%d&year=%d", baseURL, weekNum, year)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "LongBox/1.0 (Comic Library Manager)")

	slog.Debug("walksoftly api request", "week", weekNum, "year", year)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	// Handle walksoftly-specific status codes
	switch resp.StatusCode {
	case http.StatusOK:
		// success, continue
	case 522:
		return nil, fmt.Errorf("walksoftly service is offline")
	case 619:
		return nil, fmt.Errorf("invalid date parameters (week=%d, year=%d)", weekNum, year)
	case 666:
		return nil, fmt.Errorf("walksoftly reports client update required")
	default:
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("walksoftly returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	var releases []Release
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	slog.Info("fetched weekly releases from walksoftly",
		"week", weekNum,
		"year", year,
		"count", len(releases),
	)

	return releases, nil
}

// WeekNumber computes the Sunday-based week number for a given date,
// matching Python's strftime("%U") which the walksoftly API expects.
// Week 0 contains days before the first Sunday of the year.
func WeekNumber(t time.Time) int {
	yday := t.YearDay() // 1-366
	weekday := int(t.Weekday()) // Sunday=0, Monday=1, ..., Saturday=6
	// strftime %U: (yday + 7 - weekday) / 7, but using 0-based yday
	return (yday + 6 - weekday) / 7
}

// DateToWeek converts a date string (YYYY-MM-DD) to a walksoftly week number and year.
// It uses the Wednesday of the given week (comic book day) for the calculation,
// since the frontend sends Monday-Sunday ranges but walksoftly weeks are Sunday-based.
func DateToWeek(dateStr string) (weekNum int, year int, err error) {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return 0, 0, fmt.Errorf("parsing date %q: %w", dateStr, err)
	}

	// Find Wednesday of this week (comic book day).
	// The frontend sends Monday as start, so Wednesday = start + 2 days.
	// But if the date is already past Wednesday, still use Wednesday.
	weekday := int(t.Weekday()) // Sunday=0, Monday=1, ..., Saturday=6
	var wednesday time.Time
	if weekday == 0 {
		// Sunday: Wednesday is 3 days ahead
		wednesday = t.AddDate(0, 0, 3)
	} else if weekday <= 3 {
		// Monday-Wednesday: move forward to Wednesday
		wednesday = t.AddDate(0, 0, 3-weekday)
	} else {
		// Thursday-Saturday: move back to Wednesday
		wednesday = t.AddDate(0, 0, -(weekday - 3))
	}

	return WeekNumber(wednesday), wednesday.Year(), nil
}

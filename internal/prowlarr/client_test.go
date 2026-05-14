package prowlarr

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const testAPIKey = "test-api-key-abc123"

// newTestClient builds a Client pointed at the given httptest server with rate
// limiting disabled (minInterval=0) so tests run without sleep delays.
func newTestClient(serverURL string) *Client {
	c := NewClient(serverURL, testAPIKey, "")
	c.limiter.minInterval = 0
	return c
}

// --- SearchIssue tests ---

func TestSearchIssue_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{
				"guid":        "nzb-guid-001",
				"indexerId":   42,
				"indexer":     "NZBGeek",
				"title":       "Amazing Spider-Man #1",
				"size":        int64(10485760),
				"publishDate": "2024-03-15T12:00:00Z",
				"downloadUrl": "https://example.com/dl/001.nzb",
				"protocol":    "usenet",
			},
			{
				"guid":        "nzb-guid-002",
				"indexerId":   43,
				"indexer":     "NZBIndex",
				"title":       "Amazing Spider-Man #1 (variant)",
				"size":        int64(8388608),
				"publishDate": "2024-03-16T08:30:00Z",
				"downloadUrl": "https://example.com/dl/002.nzb",
				"protocol":    "usenet",
			},
		})
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)
	releases, err := c.SearchIssue(context.Background(), "Amazing Spider-Man", "#1", 2024)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("expected 2 releases, got %d", len(releases))
	}

	r0 := releases[0]
	if r0.GUID != "nzb-guid-001" {
		t.Errorf("GUID: got %q, want %q", r0.GUID, "nzb-guid-001")
	}
	if r0.IndexerID != 42 {
		t.Errorf("IndexerID: got %d, want 42", r0.IndexerID)
	}
	if r0.Indexer != "NZBGeek" {
		t.Errorf("Indexer: got %q, want %q", r0.Indexer, "NZBGeek")
	}
	if r0.Title != "Amazing Spider-Man #1" {
		t.Errorf("Title: got %q", r0.Title)
	}
	if r0.Size != 10485760 {
		t.Errorf("Size: got %d, want 10485760", r0.Size)
	}
	if r0.Protocol != "usenet" {
		t.Errorf("Protocol: got %q, want %q", r0.Protocol, "usenet")
	}
	if r0.DownloadURL != "https://example.com/dl/001.nzb" {
		t.Errorf("DownloadURL: got %q", r0.DownloadURL)
	}

	wantTime := time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC)
	if !r0.PublishDate.Equal(wantTime) {
		t.Errorf("PublishDate: got %v, want %v", r0.PublishDate, wantTime)
	}
}

func TestSearchIssue_Empty(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)
	releases, err := c.SearchIssue(context.Background(), "Obscure Title", "#999", 1990)
	if err != nil {
		t.Fatalf("empty results should not be an error, got: %v", err)
	}
	if len(releases) != 0 {
		t.Errorf("expected empty slice, got %d releases", len(releases))
	}
}

func TestSearchIssue_NonTwoXX(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)
	_, err := c.SearchIssue(context.Background(), "X-Men", "#1", 2020)
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention status 500, got: %v", err)
	}
}

func TestSearchIssue_CorrectRequest(t *testing.T) {
	var capturedReq *http.Request
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("[]"))
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)
	_, err := c.SearchIssue(context.Background(), "Batman", "#50", 2019)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq == nil {
		t.Fatal("server was never called")
	}

	// Check API key header.
	if got := capturedReq.Header.Get("X-Api-Key"); got != testAPIKey {
		t.Errorf("X-Api-Key: got %q, want %q", got, testAPIKey)
	}

	params, err := url.ParseQuery(capturedReq.URL.RawQuery)
	if err != nil {
		t.Fatalf("parsing query params: %v", err)
	}

	if got := params.Get("query"); got != "Batman #50" {
		t.Errorf("query param: got %q, want %q", got, "Batman #50")
	}
	if got := params.Get("type"); got != "search" {
		t.Errorf("type param: got %q, want %q", got, "search")
	}
	if got := params.Get("categories"); got != defaultCategory {
		t.Errorf("categories param: got %q, want %q", got, defaultCategory)
	}
}

// --- GrabRelease tests ---

func TestGrabRelease_Success(t *testing.T) {
	var capturedBody []byte
	var capturedReq *http.Request
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedReq = r
		capturedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)
	err := c.GrabRelease(context.Background(), "nzb-guid-001", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedReq == nil {
		t.Fatal("server was never called")
	}
	if capturedReq.Method != http.MethodPost {
		t.Errorf("method: got %q, want POST", capturedReq.Method)
	}

	// Check API key.
	if got := capturedReq.Header.Get("X-Api-Key"); got != testAPIKey {
		t.Errorf("X-Api-Key: got %q, want %q", got, testAPIKey)
	}

	// Check Content-Type header.
	if got := capturedReq.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", got, "application/json")
	}

	// Check JSON body fields.
	var body map[string]any
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatalf("parsing request body: %v", err)
	}
	if got, _ := body["guid"].(string); got != "nzb-guid-001" {
		t.Errorf("body guid: got %q, want %q", got, "nzb-guid-001")
	}
	// JSON numbers decode to float64 by default.
	if got, _ := body["indexerId"].(float64); int(got) != 42 {
		t.Errorf("body indexerId: got %v, want 42", body["indexerId"])
	}
}

func TestGrabRelease_NonTwoXX(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)
	err := c.GrabRelease(context.Background(), "nzb-guid-bad", 99)
	if err == nil {
		t.Fatal("expected error for 400, got nil")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention status 400, got: %v", err)
	}
}

// --- TestConnection tests ---

func TestTestConnection_OK(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"appName":"Prowlarr","version":"1.14.0"}`))
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)
	if err := c.TestConnection(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestTestConnection_Unauthorized(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer ts.Close()

	c := newTestClient(ts.URL)
	err := c.TestConnection(context.Background())
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention status 401, got: %v", err)
	}
}

// --- Network failure test ---

func TestSearchIssue_NetworkFailure(t *testing.T) {
	// Point at a port that refuses connections. Use a short-lived context to
	// keep the test fast.
	c := NewClient("http://127.0.0.1:19999", testAPIKey, "")
	c.limiter.minInterval = 0

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := c.SearchIssue(ctx, "X-Men", "#1", 2020)
	if err == nil {
		t.Fatal("expected network error, got nil")
	}
}

// --- NewClient defaults ---

func TestNewClient_Defaults(t *testing.T) {
	c := NewClient("http://192.168.1.100:9696/", "key", "")
	if c.baseURL != "http://192.168.1.100:9696" {
		t.Errorf("trailing slash not trimmed: %q", c.baseURL)
	}
	if c.category != defaultCategory {
		t.Errorf("default category: got %q, want %q", c.category, defaultCategory)
	}
}

func TestNewClient_CustomCategory(t *testing.T) {
	c := NewClient("http://localhost:9696", "key", "7000")
	if c.category != "7000" {
		t.Errorf("custom category not set: got %q", c.category)
	}
}

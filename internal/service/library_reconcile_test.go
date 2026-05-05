package service

import (
	"strings"
	"testing"
	"time"
)

func TestShouldRefreshCV(t *testing.T) {
	cutoff := time.Date(2026, 4, 25, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name     string
		lastSync string
		want     bool
	}{
		{name: "empty means never synced", lastSync: "", want: true},
		{name: "unparseable means never synced", lastSync: "not-a-time", want: true},
		{name: "older than cutoff refreshes", lastSync: "2026-04-23T12:00:00Z", want: true},
		{name: "exactly at cutoff does not refresh", lastSync: "2026-04-25T00:00:00Z", want: false},
		{name: "newer than cutoff does not refresh", lastSync: "2026-04-26T00:00:00Z", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shouldRefreshCV(tc.lastSync, cutoff)
			if got != tc.want {
				t.Fatalf("shouldRefreshCV(%q, cutoff) = %v, want %v", tc.lastSync, got, tc.want)
			}
		})
	}
}

func TestStringPtrValue(t *testing.T) {
	if got := stringPtrValue(nil); got != "" {
		t.Fatalf("nil pointer should return empty string, got %q", got)
	}
	v := "hello"
	if got := stringPtrValue(&v); got != "hello" {
		t.Fatalf("got %q, want %q", got, "hello")
	}
}

func TestScanSummaryMessage(t *testing.T) {
	cases := []struct {
		name     string
		result   *ScanResult
		contains []string
	}{
		{
			name:     "minimal result shows found and added",
			result:   &ScanResult{FilesFound: 100, FilesAdded: 5},
			contains: []string{"found 100", "added 5"},
		},
		{
			name: "removals included",
			result: &ScanResult{FilesFound: 100, FilesAdded: 0, FilesRemoved: 3},
			contains: []string{"removed 3"},
		},
		{
			name: "CV reconcile fields included only when nonzero",
			result: &ScanResult{
				FilesFound:         100,
				FilesAdded:         2,
				SeriesRefreshed:    4,
				IssuesNewlyMissing: 12,
				BacklogRunsCreated: 2,
			},
			contains: []string{"CV refreshed 4", "12 newly missing", "2 backlog runs"},
		},
		{
			name:     "errors surfaced",
			result:   &ScanResult{FilesFound: 10, FilesAdded: 1, Errors: 2},
			contains: []string{"2 errors"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scanSummaryMessage(tc.result)
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("summary %q missing %q", got, want)
				}
			}
		})
	}
}

func TestScanSummaryMessageOmitsZeroOptionalCounts(t *testing.T) {
	got := scanSummaryMessage(&ScanResult{FilesFound: 1, FilesAdded: 0})
	for _, forbidden := range []string{"removed", "CV refreshed", "newly missing", "backlog runs", "errors"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("summary %q should not contain %q", got, forbidden)
		}
	}
}

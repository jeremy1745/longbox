package service

import (
	"testing"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/newznab"
)

// TestScoreResult_RejectsDistinguishingPrefix locks in the Zorro fix:
// "Lady Rawhide - Lady Zorro 001 (2015)" must not score above the
// auto-grab threshold (50) when the tracked series is plain "Zorro".
// Previously the substring-contains check awarded +50 for any title
// containing "zorro" anywhere, letting Lady Zorro slip through.
func TestScoreResult_RejectsDistinguishingPrefix(t *testing.T) {
	year := 1952
	series := &model.Series{Title: "Zorro", Year: &year}
	issue := &model.Issue{IssueNumber: "1", SortNumber: 1}

	result := newznab.SearchResult{
		Title: "Lady Rawhide - Lady Zorro 001 (2015) (digital) (Son of Ultron-Empire)",
		Size:  60 * 1024 * 1024,
	}

	got := scoreResult(result, series, issue)
	if got >= 50 {
		t.Fatalf("Lady Zorro should not pass auto-grab threshold; got score=%d", got)
	}
}

func TestScoreResult_CleanMatchPasses(t *testing.T) {
	year := 2026
	series := &model.Series{Title: "Zorro", Year: &year}
	issue := &model.Issue{IssueNumber: "1", SortNumber: 1}

	result := newznab.SearchResult{
		Title: "Zorro 001 (2026) (digital) (some-group)",
		Size:  60 * 1024 * 1024,
		Grabs: 50,
	}

	got := scoreResult(result, series, issue)
	if got < 50 {
		t.Fatalf("clean Zorro 2026 match should pass threshold; got score=%d", got)
	}
}

func TestScoreResult_OneExtraWordIsTolerated(t *testing.T) {
	// "Zorro Adventures" against a "Zorro" series should be mildly suspicious
	// but not hard-rejected — could legitimately be the same series in some
	// indexer naming conventions.
	year := 2026
	series := &model.Series{Title: "Zorro", Year: &year}
	issue := &model.Issue{IssueNumber: "1", SortNumber: 1}

	result := newznab.SearchResult{
		Title: "Zorro Adventures 001 (2026)",
		Size:  60 * 1024 * 1024,
	}

	got := scoreResult(result, series, issue)
	if got <= -50 {
		t.Fatalf("one-extra-word title should not be hard-rejected; got score=%d", got)
	}
}

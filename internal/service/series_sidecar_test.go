package service

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jeremy/longbox/internal/model"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func ptr[T any](v T) *T { return &v }

func makeTestSeries() *model.Series {
	return &model.Series{
		ID:            1,
		Title:         "Batman",
		SortTitle:     "Batman",
		Year:          ptr(1940),
		ComicVineID:   ptr[int64](1699),
		MetronID:      ptr[int64](200),
		Description:   "The Dark Knight fights crime in Gotham City.",
		TotalIssues:   713,
		CoverImageURL: "https://example.com/batman.jpg",
		PublisherName: "DC Comics",
	}
}

func makeTestIssues() []model.Issue {
	return []model.Issue{
		{
			ID:          10,
			SeriesID:    1,
			IssueNumber: "1",
			Title:       "The Bat-Man of Gotham",
			ComicVineID: ptr[int64](40001),
			MetronID:    ptr[int64](5001),
			Description: "Batman faces his greatest challenge.",
			CoverDate:   "1940-04-01",
			StoreDate:   "1940-03-15",
			Writers:     "Bob Kane",
			Artists:     "Bob Kane",
		},
		{
			ID:          11,
			SeriesID:    1,
			IssueNumber: "2",
			// No title, no description, no dates, no credits — minimal issue
		},
		{
			ID:          12,
			SeriesID:    1,
			IssueNumber: "Annual 1",
			Title:       "Annual Special",
			CoverDate:   "1961-01-01",
			Writers:     "Bill Finger",
			Artists:     "Sheldon Moldoff",
		},
	}
}

// ── WriteSeriesSidecar: seriesDir missing ─────────────────────────────────────

func TestSeriesSidecar_MissingDir(t *testing.T) {
	err := WriteSeriesSidecar("/nonexistent/path/that/does/not/exist", makeTestSeries(), nil)
	if err == nil {
		t.Fatal("expected error for nonexistent seriesDir, got nil")
	}
}

// ── ComicVine.xml — well-formed and correct content ───────────────────────────

func TestSeriesSidecar_ComicVineXML_WellFormed(t *testing.T) {
	dir := t.TempDir()
	series := makeTestSeries()

	if err := WriteSeriesSidecar(dir, series, nil); err != nil {
		t.Fatalf("WriteSeriesSidecar: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "ComicVine.xml"))
	if err != nil {
		t.Fatalf("reading ComicVine.xml: %v", err)
	}

	// Must be valid XML.
	var doc struct {
		XMLName xml.Name `xml:"Series"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("ComicVine.xml is not valid XML: %v\n%s", err, data)
	}
}

func TestSeriesSidecar_ComicVineXML_Elements(t *testing.T) {
	dir := t.TempDir()
	series := makeTestSeries()

	if err := WriteSeriesSidecar(dir, series, nil); err != nil {
		t.Fatalf("WriteSeriesSidecar: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "ComicVine.xml"))
	if err != nil {
		t.Fatalf("reading ComicVine.xml: %v", err)
	}
	body := string(data)

	checks := []struct {
		name    string
		snippet string
	}{
		{"Name", "<Name>Batman</Name>"},
		{"SortName", "<SortName>Batman</SortName>"},
		{"Year", "<Year>1940</Year>"},
		{"Publisher", "<Publisher>DC Comics</Publisher>"},
		{"ComicVineID", "<ComicVineID>1699</ComicVineID>"},
		{"MetronID", "<MetronID>200</MetronID>"},
		{"CountOfIssues", "<CountOfIssues>713</CountOfIssues>"},
		{"ImageURL", "<ImageURL>https://example.com/batman.jpg</ImageURL>"},
		{"LastUpdated element", "<LastUpdated>"},
		{"Description CDATA", "Dark Knight"},
	}
	for _, c := range checks {
		if !strings.Contains(body, c.snippet) {
			t.Errorf("ComicVine.xml missing %s: expected to find %q\n%s", c.name, c.snippet, body)
		}
	}
}

// Elements absent when source data is nil/empty.
func TestSeriesSidecar_ComicVineXML_OmitsEmptyElements(t *testing.T) {
	dir := t.TempDir()
	series := &model.Series{
		ID:    2,
		Title: "Minimal Series",
		// No MetronID, no Year, no Publisher, no Description, no CoverImageURL
		// TotalIssues == 0 → CountOfIssues omitted
	}

	if err := WriteSeriesSidecar(dir, series, nil); err != nil {
		t.Fatalf("WriteSeriesSidecar: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "ComicVine.xml"))
	if err != nil {
		t.Fatalf("reading ComicVine.xml: %v", err)
	}
	body := string(data)

	absent := []string{
		"<MetronID>", "<Year>", "<Publisher>", "<Description>",
		"<ImageURL>", "<CountOfIssues>", "<ComicVineID>",
	}
	for _, tag := range absent {
		if strings.Contains(body, tag) {
			t.Errorf("ComicVine.xml should NOT contain %q for empty data\n%s", tag, body)
		}
	}
}

// ── Per-issue .md ─────────────────────────────────────────────────────────────

func TestSeriesSidecar_IssueMD_Created(t *testing.T) {
	dir := t.TempDir()
	series := makeTestSeries()
	issues := makeTestIssues()

	if err := WriteSeriesSidecar(dir, series, issues); err != nil {
		t.Fatalf("WriteSeriesSidecar: %v", err)
	}

	// Issue #1 should be at root.
	data, err := os.ReadFile(filepath.Join(dir, "1.md"))
	if err != nil {
		t.Fatalf("reading 1.md: %v", err)
	}
	body := string(data)

	checks := []struct {
		name    string
		snippet string
	}{
		{"heading", "# Batman #1"},
		{"cover date", "**Cover Date:** 1940-04-01"},
		{"store date", "**Store Date:** 1940-03-15"},
		{"title", "**Title:** The Bat-Man of Gotham"},
		{"recap section", "## Recap"},
		{"description text", "Batman faces his greatest challenge."},
		{"credits section", "## Credits"},
		{"writer", "- Writer: Bob Kane"},
		{"artist", "- Artist: Bob Kane"},
		{"metron id footer", "Metron ID: 5001"},
		{"comicvine id footer", "ComicVine ID: 40001"},
	}
	for _, c := range checks {
		if !strings.Contains(body, c.snippet) {
			t.Errorf("1.md missing %s: expected to find %q\n%s", c.name, c.snippet, body)
		}
	}
}

// Minimal issue — no title, no description, no dates, no credits.
// Those fields must NOT appear in the output.
func TestSeriesSidecar_IssueMD_OmitsEmptyFields(t *testing.T) {
	dir := t.TempDir()
	series := makeTestSeries()
	issues := []model.Issue{
		{
			ID:          20,
			SeriesID:    1,
			IssueNumber: "3",
			// Everything else empty
		},
	}

	if err := WriteSeriesSidecar(dir, series, issues); err != nil {
		t.Fatalf("WriteSeriesSidecar: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "3.md"))
	if err != nil {
		t.Fatalf("reading 3.md: %v", err)
	}
	body := string(data)

	// Heading must be present.
	if !strings.Contains(body, "# Batman #3") {
		t.Errorf("3.md missing heading\n%s", body)
	}

	absent := []string{
		"**Cover Date:**", "**Store Date:**", "**Title:**",
		"## Recap", "## Credits", "---",
		"Metron ID:", "ComicVine ID:",
	}
	for _, tag := range absent {
		if strings.Contains(body, tag) {
			t.Errorf("3.md should NOT contain %q for empty data\n%s", tag, body)
		}
	}
}

// ── Annual routing ────────────────────────────────────────────────────────────

func TestSeriesSidecar_AnnualGoesInSubdir(t *testing.T) {
	dir := t.TempDir()
	series := makeTestSeries()
	issues := []model.Issue{
		{
			ID:          30,
			SeriesID:    1,
			IssueNumber: "Annual 1",
			Title:       "Annual Special",
		},
	}

	if err := WriteSeriesSidecar(dir, series, issues); err != nil {
		t.Fatalf("WriteSeriesSidecar: %v", err)
	}

	// Must be in Annuals/ subdir.
	annualPath := filepath.Join(dir, "Annuals", "Annual 1.md")
	if _, err := os.Stat(annualPath); os.IsNotExist(err) {
		t.Fatalf("expected annual md at %s, not found", annualPath)
	}

	// Must NOT be in root.
	rootPath := filepath.Join(dir, "Annual 1.md")
	if _, err := os.Stat(rootPath); err == nil {
		t.Fatalf("annual md should NOT be at root %s", rootPath)
	}
}

func TestSeriesSidecar_AnnualsSubdirCreated(t *testing.T) {
	dir := t.TempDir()
	series := makeTestSeries()
	issues := []model.Issue{
		{ID: 31, SeriesID: 1, IssueNumber: "Annual 2"},
	}

	if err := WriteSeriesSidecar(dir, series, issues); err != nil {
		t.Fatalf("WriteSeriesSidecar: %v", err)
	}

	annualsDir := filepath.Join(dir, "Annuals")
	if fi, err := os.Stat(annualsDir); err != nil || !fi.IsDir() {
		t.Fatalf("Annuals/ subdir should have been created at %s", annualsDir)
	}
}

// ── Filename sanitization ─────────────────────────────────────────────────────

func TestSeriesSidecar_UnsafeIssueNumberFilename(t *testing.T) {
	dir := t.TempDir()
	series := makeTestSeries()
	issues := []model.Issue{
		{ID: 40, SeriesID: 1, IssueNumber: "un#ed"},
	}

	if err := WriteSeriesSidecar(dir, series, issues); err != nil {
		t.Fatalf("WriteSeriesSidecar: %v", err)
	}

	// The file should exist under some sanitized name — no error, and the file
	// is somewhere in dir (not necessarily "un#ed.md" verbatim since # is unsafe).
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading dir: %v", err)
	}
	var mdFiles []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".md") {
			mdFiles = append(mdFiles, e.Name())
		}
	}
	if len(mdFiles) == 0 {
		t.Fatal("expected a .md file for un#ed issue, found none")
	}
	// The sanitized filename must not contain the raw '#'.
	for _, name := range mdFiles {
		if strings.Contains(name, "#") {
			t.Errorf("sanitized filename still contains '#': %s", name)
		}
	}
}

func TestSanitizeIssueFilename(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"1", "1.md"},
		{"un#ed", "un_ed.md"},
		{"1/2", "1_2.md"},
		{"Special: Part 1", "Special_ Part 1.md"},
		{"", "unknown.md"},
	}
	for _, c := range cases {
		got := sanitizeIssueFilename(c.in)
		if got != c.want {
			t.Errorf("sanitizeIssueFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ── stripHTML ─────────────────────────────────────────────────────────────────

func TestStripHTML(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"<p>Hello world.</p>", "Hello world."},
		{"Line1<br>Line2", "Line1\nLine2"},
		{"<b>bold</b> text", "bold text"},
		{"no tags", "no tags"},
		{"", ""},
	}
	for _, c := range cases {
		got := stripHTML(c.in)
		if got != c.want {
			t.Errorf("stripHTML(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

package service

// series_sidecar.go writes ComicVine-compatible sidecar files into an existing
// series folder:
//
//   - ComicVine.xml  — series-level metadata dump at the folder root.
//   - <issueNum>.md  — per-issue recap+credits file at the folder root.
//                      Annual issues land under seriesDir/Annuals/ instead.
//
// The caller is responsible for creating seriesDir before calling
// WriteSeriesSidecar. This file does not create folders (except Annuals/).

import (
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/jeremy/longbox/internal/model"
)

// sidecarDirLocksMu guards sidecarDirLocks.
var sidecarDirLocksMu sync.Mutex

// sidecarDirLocks maps an absolute series directory path to its mutex.
// Package-level; same pattern as LongboxMetadataService.lockSeriesDir.
var sidecarDirLocks = make(map[string]*sync.Mutex)

// lockSidecarDir acquires a per-directory mutex and returns the unlock func.
// Call defer unlock() immediately after.
func lockSidecarDir(seriesDir string) func() {
	sidecarDirLocksMu.Lock()
	mu, ok := sidecarDirLocks[seriesDir]
	if !ok {
		mu = &sync.Mutex{}
		sidecarDirLocks[seriesDir] = mu
	}
	sidecarDirLocksMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

// WriteSeriesSidecar writes ComicVine.xml and per-issue <num>.md files into seriesDir.
// seriesDir must already exist. Annual issues' .md files go under seriesDir/Annuals/.
//
// Partial failure behaviour: if one .md write fails the error is logged and
// the rest of the issues are still processed. All errors are collected and
// returned as a single combined error at the end.
func WriteSeriesSidecar(seriesDir string, series *model.Series, issues []model.Issue) error {
	// Fail fast if the directory does not exist — caller's job to create it.
	if _, err := os.Stat(seriesDir); os.IsNotExist(err) {
		return fmt.Errorf("series_sidecar: seriesDir does not exist: %q", seriesDir)
	} else if err != nil {
		return fmt.Errorf("series_sidecar: stat seriesDir: %w", err)
	}

	unlock := lockSidecarDir(seriesDir)
	defer unlock()

	var errs []error

	// 1. Write ComicVine.xml
	if err := writeComicVineXML(seriesDir, series); err != nil {
		slog.Warn("series_sidecar: failed to write ComicVine.xml",
			"series_id", series.ID,
			"error", err,
		)
		errs = append(errs, fmt.Errorf("writing ComicVine.xml: %w", err))
	}

	// 2. Write per-issue .md files
	for i := range issues {
		if err := writeIssueMD(seriesDir, series, &issues[i]); err != nil {
			slog.Warn("series_sidecar: failed to write issue md",
				"series_id", series.ID,
				"issue_number", issues[i].IssueNumber,
				"error", err,
			)
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

// ── ComicVine.xml ─────────────────────────────────────────────────────────────

// schema: ComicVine.xml
//
// <?xml version="1.0" encoding="UTF-8"?>
// <Series>
//   <ComicVineID>12345</ComicVineID>            — series.ComicVineID (omit if nil)
//   <MetronID>6789</MetronID>                   — series.MetronID (omit if nil)
//   <Name>Series Title</Name>                   — series.Title
//   <SortName>Series Title</SortName>           — series.SortTitle (omit if empty)
//   <Year>2024</Year>                           — series.Year (omit if nil)
//   <Publisher>Publisher Name</Publisher>       — series.PublisherName (omit if empty)
//   <CountOfIssues>12</CountOfIssues>           — series.TotalIssues (omit if 0)
//   <Description><![CDATA[...]]></Description>  — series.Description, CDATA-wrapped (omit if empty)
//   <ImageURL>https://...</ImageURL>            — series.CoverImageURL (omit if empty)
//   <LastUpdated>2026-05-14T...</LastUpdated>   — current time, RFC3339
// </Series>
//
// CharacterCredits and PersonCredits are omitted — model.Series carries no such data.

// comicVineSeriesXML is the Go struct marshalled to ComicVine.xml.
// Fields use omitempty or pointer types so absent data produces no element.
type comicVineSeriesXML struct {
	XMLName      xml.Name          `xml:"Series"`
	ComicVineID  *int64            `xml:"ComicVineID,omitempty"`
	MetronID     *int64            `xml:"MetronID,omitempty"`
	Name         string            `xml:"Name"`
	SortName     string            `xml:"SortName,omitempty"`
	Year         *int              `xml:"Year,omitempty"`
	Publisher    string            `xml:"Publisher,omitempty"`
	CountOfIssues *int             `xml:"CountOfIssues,omitempty"`
	Description  *cdataString      `xml:"Description"`
	ImageURL     string            `xml:"ImageURL,omitempty"`
	LastUpdated  string            `xml:"LastUpdated"`
}

// cdataString marshals a string as a CDATA section.
type cdataString struct {
	Value string `xml:",cdata"`
}

// MarshalXML omits the element entirely when the CDATA value is empty.
// encoding/xml skips nil pointer fields before dispatching MarshalXML, so
// c is always non-nil here; only the empty-value guard is needed.
func (c *cdataString) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	if c.Value == "" {
		return nil
	}
	return e.EncodeElement(struct {
		Value string `xml:",cdata"`
	}{Value: c.Value}, start)
}

func writeComicVineXML(seriesDir string, series *model.Series) error {
	doc := comicVineSeriesXML{
		Name:        series.Title,
		SortName:    series.SortTitle,
		Year:        series.Year,
		Publisher:   series.PublisherName,
		ComicVineID: series.ComicVineID,
		MetronID:    series.MetronID,
		ImageURL:    series.CoverImageURL,
		LastUpdated: time.Now().UTC().Format(time.RFC3339),
	}

	if series.TotalIssues > 0 {
		n := series.TotalIssues
		doc.CountOfIssues = &n
	}

	desc := strings.TrimSpace(series.Description)
	if desc != "" {
		doc.Description = &cdataString{Value: desc}
	}

	data, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshalling ComicVine.xml: %w", err)
	}

	// Prepend the XML declaration.
	full := append([]byte(xml.Header), data...)
	full = append(full, '\n')

	destPath := filepath.Join(seriesDir, "ComicVine.xml")
	if _, err := writeFileAtomicIfChanged(destPath, full, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", destPath, err)
	}
	return nil
}

// ── Per-issue .md ─────────────────────────────────────────────────────────────

// unsafeFilenameChars matches characters that are unsafe in filenames across
// common filesystems (Windows, macOS, Linux). Replaced with "_".
var unsafeFilenameChars = regexp.MustCompile(`[\\/:*?"<>|#%&{}]`)

// multiUnderscoreRE matches two or more consecutive underscores.
var multiUnderscoreRE = regexp.MustCompile(`_+`)

// htmlBRRE matches <br> variants (self-closing or not, any case).
var htmlBRRE = regexp.MustCompile(`(?i)<br\s*/?>`)

// htmlMultiNewlineRE matches three or more consecutive newlines.
var htmlMultiNewlineRE = regexp.MustCompile(`\n{3,}`)

// sanitizeIssueFilename turns an issue number into a safe filename stem.
// e.g. "un#ed" → "un_ed", "1/2" → "1_2".
func sanitizeIssueFilename(issueNumber string) string {
	safe := unsafeFilenameChars.ReplaceAllString(issueNumber, "_")
	// Collapse multiple underscores that might result from adjacent unsafe chars.
	safe = multiUnderscoreRE.ReplaceAllString(safe, "_")
	safe = strings.Trim(safe, "_")
	if safe == "" {
		safe = "unknown"
	}
	return safe + ".md"
}

// stripHTML does a minimal HTML tag strip for description text.
// Not a full HTML parser — good enough for comic descriptions which contain
// light markup like <p>, <br>, <b>, <i>.
var htmlTagRE = regexp.MustCompile(`<[^>]+>`)

func stripHTML(s string) string {
	// Replace <br> variants with newlines before stripping other tags.
	s = htmlBRRE.ReplaceAllString(s, "\n")
	s = htmlTagRE.ReplaceAllString(s, "")
	// Collapse multiple blank lines down to two.
	s = htmlMultiNewlineRE.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

func writeIssueMD(seriesDir string, series *model.Series, issue *model.Issue) error {
	isAnnual := containsAnnual(issue.IssueNumber) || containsAnnual(issue.Title)

	targetDir := seriesDir
	if isAnnual {
		targetDir = filepath.Join(seriesDir, "Annuals")
		if err := os.MkdirAll(targetDir, 0755); err != nil {
			return fmt.Errorf("creating Annuals subdir: %w", err)
		}
	}

	filename := sanitizeIssueFilename(issue.IssueNumber)
	destPath := filepath.Join(targetDir, filename)

	content := buildIssueMD(series, issue)

	if _, err := writeFileAtomicIfChanged(destPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing issue md %s: %w", destPath, err)
	}
	return nil
}

// buildIssueMD renders the markdown body for a single issue.
func buildIssueMD(series *model.Series, issue *model.Issue) string {
	var b strings.Builder

	// Heading
	fmt.Fprintf(&b, "# %s #%s\n", series.Title, issue.IssueNumber)
	b.WriteByte('\n')

	// Meta fields — omit when empty
	if issue.CoverDate != "" {
		fmt.Fprintf(&b, "**Cover Date:** %s\n", issue.CoverDate)
	}
	if issue.StoreDate != "" {
		fmt.Fprintf(&b, "**Store Date:** %s\n", issue.StoreDate)
	}
	if strings.TrimSpace(issue.Title) != "" {
		fmt.Fprintf(&b, "**Title:** %s\n", strings.TrimSpace(issue.Title))
	}

	// Recap section — omit entirely if no description
	desc := stripHTML(strings.TrimSpace(issue.Description))
	if desc != "" {
		b.WriteByte('\n')
		b.WriteString("## Recap\n\n")
		b.WriteString(desc)
		b.WriteByte('\n')
	}

	// Credits section — omit entirely if no writers AND no artists
	writers := strings.TrimSpace(issue.Writers)
	artists := strings.TrimSpace(issue.Artists)
	if writers != "" || artists != "" {
		b.WriteByte('\n')
		b.WriteString("## Credits\n\n")
		if writers != "" {
			fmt.Fprintf(&b, "- Writer: %s\n", writers)
		}
		if artists != "" {
			fmt.Fprintf(&b, "- Artist: %s\n", artists)
		}
	}

	// Footer — only show IDs that exist
	var footerParts []string
	if issue.MetronID != nil {
		footerParts = append(footerParts, fmt.Sprintf("Metron ID: %d", *issue.MetronID))
	}
	if issue.ComicVineID != nil {
		footerParts = append(footerParts, fmt.Sprintf("ComicVine ID: %d", *issue.ComicVineID))
	}
	if len(footerParts) > 0 {
		b.WriteString("\n---\n")
		fmt.Fprintf(&b, "*%s*\n", strings.Join(footerParts, " · "))
	}

	return b.String()
}

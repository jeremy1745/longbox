package service

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jeremy/longbox/internal/model"
)

// --- CBZ test helpers ---

type testComicInfo struct {
	Series string
	Number string
	Title  string
}

// makeTestCBZ writes a minimal CBZ file at path.
// If ci is non-nil, a ComicInfo.xml is embedded; otherwise the CBZ has only a
// placeholder image. The data argument is the raw bytes of the CBZ.
func makeTestCBZ(t *testing.T, path string, ci *testComicInfo) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)

	if ci != nil {
		type xmlComicInfo struct {
			XMLName xml.Name `xml:"ComicInfo"`
			Series  string   `xml:"Series,omitempty"`
			Number  string   `xml:"Number,omitempty"`
			Title   string   `xml:"Title,omitempty"`
		}
		xmlData, err := xml.MarshalIndent(xmlComicInfo{
			Series: ci.Series,
			Number: ci.Number,
			Title:  ci.Title,
		}, "", "  ")
		if err != nil {
			t.Fatalf("marshal ComicInfo: %v", err)
		}
		xmlData = append([]byte(xml.Header), xmlData...)

		w, err := zw.Create("ComicInfo.xml")
		if err != nil {
			t.Fatalf("create ComicInfo.xml in zip: %v", err)
		}
		if _, err := w.Write(xmlData); err != nil {
			t.Fatalf("write ComicInfo.xml: %v", err)
		}
	}

	// Always add a placeholder image so the archive is structurally valid.
	w, err := zw.Create("page001.jpg")
	if err != nil {
		t.Fatalf("create page001.jpg: %v", err)
	}
	if _, err := w.Write([]byte{0xFF, 0xD8, 0xFF, 0xE0}); err != nil { // minimal JPEG magic
		t.Fatalf("write page001.jpg: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
}

// makeLargerTestCBZ writes a CBZ with extra padding so it is larger than a
// previously created file at the same path — used to verify size-based tie-breaking.
func makeLargerTestCBZ(t *testing.T, path string, ci *testComicInfo, extraBytes int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)

	if ci != nil {
		type xmlComicInfo struct {
			XMLName xml.Name `xml:"ComicInfo"`
			Series  string   `xml:"Series,omitempty"`
			Number  string   `xml:"Number,omitempty"`
			Title   string   `xml:"Title,omitempty"`
		}
		xmlData, err := xml.MarshalIndent(xmlComicInfo{
			Series: ci.Series,
			Number: ci.Number,
			Title:  ci.Title,
		}, "", "  ")
		if err != nil {
			t.Fatalf("marshal ComicInfo: %v", err)
		}
		xmlData = append([]byte(xml.Header), xmlData...)

		w, err := zw.Create("ComicInfo.xml")
		if err != nil {
			t.Fatalf("create ComicInfo.xml in zip: %v", err)
		}
		if _, err := w.Write(xmlData); err != nil {
			t.Fatalf("write ComicInfo.xml: %v", err)
		}
	}

	w, err := zw.Create("page001.jpg")
	if err != nil {
		t.Fatalf("create page001.jpg in zip: %v", err)
	}
	padding := make([]byte, extraBytes+4)
	padding[0] = 0xFF
	padding[1] = 0xD8
	padding[2] = 0xFF
	padding[3] = 0xE0
	if _, err := w.Write(padding); err != nil {
		t.Fatalf("write padding: %v", err)
	}

	if err := zw.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
}

// --- nilSeriesRepo stub ---

// nilSeriesRepo implements just enough of SeriesRepo's interface to return an
// empty series list — used when we don't want any canonical folder skipping.
type nilSeriesListFunc func() ([]model.Series, int, error)

// --- Tests ---

func TestLibraryScan_FingerprintFromComicInfo(t *testing.T) {
	dir := t.TempDir()

	cbzPath := filepath.Join(dir, "batman001.cbz")
	makeTestCBZ(t, cbzPath, &testComicInfo{
		Series: "Batman",
		Number: "1",
	})

	svc := &LibraryScanService{seriesRepo: nil}
	series := &model.Series{Title: "Batman"}

	result, err := svc.findFilesNoSkip(dir, series)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d: %v", len(result.Matches), result.Matches)
	}
	if _, ok := result.Matches["1"]; !ok {
		t.Errorf("expected issue '1' in Matches, got: %v", result.Matches)
	}
}

func TestLibraryScan_FingerprintFromFilename(t *testing.T) {
	dir := t.TempDir()

	// No ComicInfo.xml — relies on filename parsing.
	cbzPath := filepath.Join(dir, "Batman 003 (2016).cbz")
	makeTestCBZ(t, cbzPath, nil)

	svc := &LibraryScanService{seriesRepo: nil}
	series := &model.Series{Title: "Batman"}

	result, err := svc.findFilesNoSkip(dir, series)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d: %v", len(result.Matches), result.Matches)
	}
	if _, ok := result.Matches["3"]; !ok {
		t.Errorf("expected issue '3' in Matches, got: %v", result.Matches)
	}
}

func TestLibraryScan_Normalization(t *testing.T) {
	dir := t.TempDir()

	// ComicInfo series name differs only by colon and trailing punctuation.
	cbzPath := filepath.Join(dir, "30_days_01.cbz")
	makeTestCBZ(t, cbzPath, &testComicInfo{
		Series: "30 Days of Night: Falling Sun",
		Number: "1",
	})

	svc := &LibraryScanService{seriesRepo: nil}
	// Target series uses no colon — normalization should still match.
	series := &model.Series{Title: "30 Days of Night Falling Sun"}

	result, err := svc.findFilesNoSkip(dir, series)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match after normalization, got %d: %v", len(result.Matches), result.Matches)
	}
}

func TestLibraryScan_NonMatchingSeriesSkipped(t *testing.T) {
	dir := t.TempDir()

	cbzPath := filepath.Join(dir, "superman001.cbz")
	makeTestCBZ(t, cbzPath, &testComicInfo{
		Series: "Superman",
		Number: "1",
	})

	svc := &LibraryScanService{seriesRepo: nil}
	series := &model.Series{Title: "Batman"}

	result, err := svc.findFilesNoSkip(dir, series)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Matches) != 0 {
		t.Errorf("expected 0 matches for wrong series, got %d: %v", len(result.Matches), result.Matches)
	}
}

func TestLibraryScan_AnnualDetection(t *testing.T) {
	dir := t.TempDir()

	// File with "Annual" in the filename — no ComicInfo.
	// Use dash-before-Annual format so the parser yields series="Batman".
	cbzPath := filepath.Join(dir, "Batman - Annual 01.cbz")
	makeTestCBZ(t, cbzPath, nil)

	svc := &LibraryScanService{seriesRepo: nil}
	series := &model.Series{Title: "Batman"}

	result, err := svc.findFilesNoSkip(dir, series)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Matches) != 0 {
		t.Errorf("expected 0 regular matches, got %d", len(result.Matches))
	}
	if len(result.Annuals) != 1 {
		t.Fatalf("expected 1 annual, got %d: %v", len(result.Annuals), result.Annuals)
	}
}

func TestLibraryScan_AnnualDetectionViaComicInfo(t *testing.T) {
	dir := t.TempDir()

	// ComicInfo title signals annual.
	cbzPath := filepath.Join(dir, "batman_special.cbz")
	makeTestCBZ(t, cbzPath, &testComicInfo{
		Series: "Batman",
		Number: "Annual 2",
		Title:  "Batman Annual 2016",
	})

	svc := &LibraryScanService{seriesRepo: nil}
	series := &model.Series{Title: "Batman"}

	result, err := svc.findFilesNoSkip(dir, series)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Matches) != 0 {
		t.Errorf("expected 0 regular matches, got %d", len(result.Matches))
	}
	if len(result.Annuals) != 1 {
		t.Fatalf("expected 1 annual via ComicInfo, got %d: %v", len(result.Annuals), result.Annuals)
	}
}

func TestLibraryScan_DuplicateResolution_ComicInfoWins(t *testing.T) {
	dir := t.TempDir()

	// Two files for issue #5: one with ComicInfo, one without.
	withCI := filepath.Join(dir, "Batman 005 (2016).cbz")
	makeTestCBZ(t, withCI, &testComicInfo{
		Series: "Batman",
		Number: "5",
	})

	noCI := filepath.Join(dir, "Batman (2016) #005.cbz")
	makeTestCBZ(t, noCI, nil)

	svc := &LibraryScanService{seriesRepo: nil}
	series := &model.Series{Title: "Batman"}

	result, err := svc.findFilesNoSkip(dir, series)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match (winner), got %d: %v", len(result.Matches), result.Matches)
	}

	winner := result.Matches["5"]
	if winner != withCI {
		t.Errorf("expected ComicInfo file to win: got %q, want %q", winner, withCI)
	}

	if len(result.RejectedDuplicates) != 1 {
		t.Fatalf("expected 1 rejected duplicate, got %d: %v", len(result.RejectedDuplicates), result.RejectedDuplicates)
	}
	if result.RejectedDuplicates[0] != noCI {
		t.Errorf("expected no-CI file to be rejected: got %q, want %q",
			result.RejectedDuplicates[0], noCI)
	}
}

func TestLibraryScan_DuplicateResolution_LargerFileWins(t *testing.T) {
	dir := t.TempDir()

	// Both files have ComicInfo — larger should win.
	smallFile := filepath.Join(dir, "Batman 007 small.cbz")
	makeTestCBZ(t, smallFile, &testComicInfo{
		Series: "Batman",
		Number: "7",
	})

	bigFile := filepath.Join(dir, "Batman 007 big.cbz")
	makeLargerTestCBZ(t, bigFile, &testComicInfo{
		Series: "Batman",
		Number: "7",
	}, 50000)

	svc := &LibraryScanService{seriesRepo: nil}
	series := &model.Series{Title: "Batman"}

	result, err := svc.findFilesNoSkip(dir, series)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d: %v", len(result.Matches), result.Matches)
	}

	winner := result.Matches["7"]
	if winner != bigFile {
		t.Errorf("expected larger file to win: got %q, want %q", winner, bigFile)
	}

	if len(result.RejectedDuplicates) != 1 {
		t.Fatalf("expected 1 rejected, got %d", len(result.RejectedDuplicates))
	}
}

func TestLibraryScan_CanonicalFoldersSkipped(t *testing.T) {
	dir := t.TempDir()

	// Create a canonical series folder — it will be in the skip set.
	canonicalDir := filepath.Join(dir, "Batman (2016)")
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Drop a Batman file inside the canonical folder.
	insideCanon := filepath.Join(canonicalDir, "Batman 001.cbz")
	makeTestCBZ(t, insideCanon, &testComicInfo{
		Series: "Batman",
		Number: "1",
	})

	// Drop a different Batman file outside any canonical folder.
	outsideCanon := filepath.Join(dir, "Batman 002.cbz")
	makeTestCBZ(t, outsideCanon, &testComicInfo{
		Series: "Batman",
		Number: "2",
	})

	year := 2016
	svc := &LibraryScanService{seriesRepo: nil}

	// Override the canonical folder set manually — normally the repo provides
	// this, but here we inject it directly via the internal helper.
	canonicalSet := map[string]bool{
		"Batman (2016)": true,
	}
	result, err := svc.findFilesWithSkipSet(dir, &model.Series{Title: "Batman", Year: &year}, canonicalSet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Issue 1 is in the canonical folder (skipped), issue 2 is outside.
	if _, ok := result.Matches["1"]; ok {
		t.Error("expected issue 1 (inside canonical folder) to be skipped")
	}
	if _, ok := result.Matches["2"]; !ok {
		t.Error("expected issue 2 (outside canonical folder) to be found")
	}
}

// --- Internal helpers exposed for testing ---

// findFilesNoSkip is a test helper that calls FindFilesForSeries with an empty
// canonical folder skip set (no seriesRepo needed).
func (s *LibraryScanService) findFilesNoSkip(
	libraryDir string,
	series *model.Series,
) (FindFilesResult, error) {
	return s.findFilesWithSkipSet(libraryDir, series, map[string]bool{})
}

// findFilesWithSkipSet is the actual walk logic factored out so tests can
// inject a pre-built canonical folder set without a live DB.
func (s *LibraryScanService) findFilesWithSkipSet(
	libraryDir string,
	series *model.Series,
	canonicalFolders map[string]bool,
) (FindFilesResult, error) {
	result := FindFilesResult{
		Matches: make(map[string]string),
		Annuals: make(map[string]string),
	}

	targetKey := normalizeSeriesTitle(series.Title)

	matchesCand := make(map[string]fileFingerprint)
	annualsCand := make(map[string]fileFingerprint)

	err := filepath.WalkDir(libraryDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			slog.Warn("library scan: walk error", "path", path, "error", walkErr)
			return nil
		}

		if d.IsDir() {
			if path == libraryDir {
				return nil
			}
			baseName := filepath.Base(path)
			if canonicalFolders[baseName] {
				return filepath.SkipDir
			}
			return nil
		}

		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".cbz" && ext != ".cbr" {
			return nil
		}

		fp, ok := fingerprintFile(path)
		if !ok {
			return nil
		}

		if normalizeSeriesTitle(fp.series) != targetKey {
			return nil
		}

		if fp.number == "" {
			return nil
		}

		issueNum := normalizeIssueNumber(fp.number)

		if fp.isAnnual {
			resolveDuplicate(annualsCand, issueNum, fp, &result.RejectedDuplicates)
		} else {
			resolveDuplicate(matchesCand, issueNum, fp, &result.RejectedDuplicates)
		}
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("walking library dir %q: %w", libraryDir, err)
	}

	for num, fp := range matchesCand {
		result.Matches[num] = fp.absPath
	}
	for num, fp := range annualsCand {
		result.Annuals[num] = fp.absPath
	}
	return result, nil
}

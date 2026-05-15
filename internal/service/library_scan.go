package service

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jeremy/longbox/internal/archive"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"github.com/jeremy/longbox/internal/scanner"
)

// FindFilesResult holds the output of a library scan for one series.
type FindFilesResult struct {
	// Matches maps normalized issue number → absolute file path for
	// regular (non-annual) issues belonging to the target series.
	Matches map[string]string

	// Annuals maps normalized issue number → absolute file path for
	// files identified as annuals (ComicInfo.xml <Format> == "Annual"
	// or "Annual" in the filename). These go to an Annuals/ subfolder.
	Annuals map[string]string

	// RejectedDuplicates lists absolute paths of files that lost a
	// duplicate-resolution contest (same issue number, same bucket).
	RejectedDuplicates []string
}

// LibraryScanService finds existing comic files belonging to a given series.
type LibraryScanService struct {
	seriesRepo *repository.SeriesRepo
}

// NewLibraryScanService constructs a LibraryScanService.
func NewLibraryScanService(seriesRepo *repository.SeriesRepo) *LibraryScanService {
	return &LibraryScanService{seriesRepo: seriesRepo}
}

// fileFingerprint captures metadata extracted from a single comic file.
type fileFingerprint struct {
	series     string
	number     string
	isAnnual   bool
	hasCI      bool // true if ComicInfo.xml was present
	fileSize   int64
	absPath    string
}

// FindFilesForSeries walks libraryDir for .cbz/.cbr files belonging to series,
// returning a FindFilesResult with matched regular issues, annuals, and
// rejected duplicates.
//
// The walk opens every archive in the library to fingerprint it — on a large
// SMB-mounted library that is genuinely slow (minutes). The ctx lets the
// caller bound it: cancellation aborts the walk promptly and returns ctx.Err().
func (s *LibraryScanService) FindFilesForSeries(
	ctx context.Context,
	libraryDir string,
	series *model.Series,
) (FindFilesResult, error) {

	// Build the set of canonical folder names for ALL series in the DB.
	// We skip those subdirectories during the walk so we never confuse a
	// Batman file sitting inside "Batman (2016)" with our target series.
	canonicalFolders, err := s.canonicalFolderSet()
	if err != nil {
		return FindFilesResult{}, fmt.Errorf("building canonical folder set: %w", err)
	}

	return s.findFilesWithSkipSet(ctx, libraryDir, series, canonicalFolders)
}

// findFilesWithSkipSet performs the actual directory walk, skipping any
// subdirectory whose base name appears in canonicalFolders. The walk checks
// ctx at every entry so a cancelled/timed-out caller aborts promptly.
//
// NOTE: filepath.WalkDir does not follow symlinks. Symlinked subdirectories on
// the network share (SMB mount) are silently skipped. If that becomes a
// problem, replace this with a manual recursive walk using os.ReadLink +
// os.Stat to dereference symlinks before descending.
func (s *LibraryScanService) findFilesWithSkipSet(
	ctx context.Context,
	libraryDir string,
	series *model.Series,
	canonicalFolders map[string]bool,
) (FindFilesResult, error) {
	result := FindFilesResult{
		Matches: make(map[string]string),
		Annuals: make(map[string]string),
	}

	targetKey := normalizeSeriesTitle(series.Title)

	// matchesCand[issueNum] = best fingerprint seen so far
	// annualsCand[issueNum] = best annual fingerprint seen so far
	matchesCand := make(map[string]fileFingerprint)
	annualsCand := make(map[string]fileFingerprint)

	err := filepath.WalkDir(libraryDir, func(path string, d os.DirEntry, walkErr error) error {
		// Abort promptly if the caller's context was cancelled or timed out —
		// returning the ctx error stops WalkDir and surfaces from below.
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			slog.Warn("library scan: walk error", "path", path, "error", walkErr)
			return nil // continue walking, don't abort
		}

		if d.IsDir() {
			// Skip the library root itself.
			if path == libraryDir {
				return nil
			}
			// Skip canonical series folders — they are already organized.
			// Only compare the base name, not the full path.
			baseName := filepath.Base(path)
			if canonicalFolders[baseName] {
				return filepath.SkipDir
			}
			return nil
		}

		// Only process .cbz and .cbr files.
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".cbz" && ext != ".cbr" {
			return nil
		}

		fp, ok := fingerprintFile(path)
		if !ok {
			// Could not extract any metadata — silently skip.
			return nil
		}

		// Check if this file belongs to the target series.
		if normalizeSeriesTitle(fp.series) != targetKey {
			return nil
		}

		if fp.number == "" {
			// We know the series matches but have no issue number — skip.
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

// fingerprintFile attempts to extract series/issue metadata from a comic file.
// Returns (fingerprint, true) if at least a series name could be determined.
func fingerprintFile(path string) (fileFingerprint, bool) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	fi, err := os.Stat(absPath)
	if err != nil {
		return fileFingerprint{}, false
	}
	fileSize := fi.Size()

	// Try ComicInfo.xml first.
	if fp, ok := fingerprintFromComicInfo(absPath, fileSize); ok {
		return fp, true
	}

	// Fall back to filename parsing.
	return fingerprintFromFilename(absPath, fileSize)
}

// fingerprintFromComicInfo opens the archive and reads ComicInfo.xml.
func fingerprintFromComicInfo(path string, fileSize int64) (fileFingerprint, bool) {
	a, err := archive.Open(path)
	if err != nil {
		return fileFingerprint{}, false
	}
	defer a.Close()

	ci, err := archive.ReadComicInfo(a)
	if err != nil || ci == nil {
		return fileFingerprint{}, false
	}
	if ci.Series == "" {
		return fileFingerprint{}, false
	}

	// Detect annuals: check all ComicInfo text fields (including <Format>,
	// which is "Annual" for annual issues per the ComicInfo v2 spec) and
	// the filename itself.
	isAnnual := containsAnnual(ci.Format) ||
		containsAnnual(ci.Series) ||
		containsAnnual(ci.Title) ||
		containsAnnual(ci.Number) ||
		containsAnnual(filepath.Base(path))

	return fileFingerprint{
		series:   ci.Series,
		number:   ci.Number,
		isAnnual: isAnnual,
		hasCI:    true,
		fileSize: fileSize,
		absPath:  path,
	}, true
}

// fingerprintFromFilename uses the scanner filename parser.
func fingerprintFromFilename(path string, fileSize int64) (fileFingerprint, bool) {
	base := filepath.Base(path)
	parsed := scanner.ParseFilename(base)
	if parsed.Series == "" {
		return fileFingerprint{}, false
	}

	isAnnual := containsAnnual(base) || containsAnnual(parsed.Number)

	return fileFingerprint{
		series:   parsed.Series,
		number:   parsed.Number,
		isAnnual: isAnnual,
		hasCI:    false,
		fileSize: fileSize,
		absPath:  path,
	}, true
}

// containsAnnual returns true if s contains the word "Annual" (case-insensitive).
func containsAnnual(s string) bool {
	return strings.Contains(strings.ToLower(s), "annual")
}

// resolveDuplicate inserts fp into cand[issueNum], applying duplicate
// resolution rules if a candidate already exists:
//  1. Prefer the file with ComicInfo.xml.
//  2. On tie, prefer the larger file.
//
// The loser's path is appended to rejected.
func resolveDuplicate(cand map[string]fileFingerprint, issueNum string, fp fileFingerprint, rejected *[]string) {
	existing, exists := cand[issueNum]
	if !exists {
		cand[issueNum] = fp
		return
	}

	winner, loser := pickWinner(existing, fp)
	if loser.absPath != "" {
		slog.Info("library scan: rejecting duplicate",
			"issue_number", issueNum,
			"rejected", loser.absPath,
			"kept", winner.absPath,
		)
		*rejected = append(*rejected, loser.absPath)
	}
	cand[issueNum] = winner
}

// pickWinner returns (winner, loser) from two fingerprints.
// Tie-break order: (1) ComicInfo-populated wins over filename-only; (2) larger
// file wins; (3) exact size tie → first seen in walk order (incumbent a).
func pickWinner(a, b fileFingerprint) (fileFingerprint, fileFingerprint) {
	// Prefer ComicInfo.xml.
	if a.hasCI && !b.hasCI {
		return a, b
	}
	if b.hasCI && !a.hasCI {
		return b, a
	}
	// Tie on ComicInfo presence — prefer larger file.
	if a.fileSize >= b.fileSize {
		return a, b
	}
	return b, a
}

// canonicalFolderSet builds the set of base folder names for all known series.
// These are the folder names we skip during the walk so we don't scan files
// that are already in their canonical location (potentially belonging to a
// different series).
func (s *LibraryScanService) canonicalFolderSet() (map[string]bool, error) {
	// List all series (paginated with a very large perPage so we get everything).
	series, _, err := s.seriesRepo.List(1, 100000, "title", "asc")
	if err != nil {
		return nil, fmt.Errorf("listing series: %w", err)
	}

	folders := make(map[string]bool, len(series))
	for i := range series {
		name := buildSeriesFolderName(series[i].Title, series[i].Year)
		if name != "" {
			folders[name] = true
		}
	}
	return folders, nil
}

// normalizeSeriesTitle produces an aggressive matching key: lowercase,
// strip all non-alphanumeric. Survives punctuation/case/whitespace differences.
// "30 Days of Night: Falling Sun" == "30 Days of Night Falling Sun".
//
// NOTE: this is algorithmically identical to repository.normalizeSeriesKey
// (package-private there, so it cannot be imported). The two MUST be kept in
// sync until they are consolidated into a shared package.
func normalizeSeriesTitle(title string) string {
	var b strings.Builder
	b.Grow(len(title))
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normalizeIssueNumber trims leading zeros and whitespace from an issue
// number string so "001" and "1" map to the same bucket.
func normalizeIssueNumber(n string) string {
	n = strings.TrimSpace(n)
	// For plain numeric strings, strip leading zeros.
	stripped := strings.TrimLeft(n, "0")
	if stripped == "" {
		// "000" → "0"
		return "0"
	}
	// Check if everything remaining is numeric (possibly with a decimal).
	allNumeric := true
	for _, r := range stripped {
		if (r < '0' || r > '9') && r != '.' {
			allNumeric = false
			break
		}
	}
	if allNumeric {
		return stripped
	}
	// Non-numeric (e.g. "Annual 1") — return as-is, normalized.
	return n
}

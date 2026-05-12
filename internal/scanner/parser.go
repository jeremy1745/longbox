package scanner

import (
	"regexp"
	"strconv"
	"strings"
)

// ParsedFilename holds metadata extracted from a comic filename.
type ParsedFilename struct {
	Series string
	Number string
	Year   int
	Volume int
}

// patterns are tried in order from most specific to least specific.
// Each pattern expects named capture groups: series, number, year, volume (all optional except series).
var patterns = []*regexp.Regexp{
	// "Batman (2016) #045 (Digital) (Zone-Empire).cbz"
	regexp.MustCompile(`(?i)^(?P<series>.+?)\s*\((?P<year>\d{4})\)\s*#(?P<number>[\d.]+(?:\s*-\s*[\d.]+)?)`),

	// "Batman #045 (2016).cbz"
	regexp.MustCompile(`(?i)^(?P<series>.+?)\s*#(?P<number>[\d.]+(?:\s*-\s*[\d.]+)?)\s*\((?P<year>\d{4})\)`),

	// "Amazing Spider-Man v2 012 (2000).cbr"
	regexp.MustCompile(`(?i)^(?P<series>.+?)\s+v(?P<volume>\d+)\s+(?P<number>[\d.]+)\s*\((?P<year>\d{4})\)`),

	// "Batman 045 (2016).cbz"
	regexp.MustCompile(`(?i)^(?P<series>.+?)\s+(?P<number>\d{2,4}(?:\.\d+)?)\s*\((?P<year>\d{4})\)`),

	// "Batman (2016) 045.cbz"
	regexp.MustCompile(`(?i)^(?P<series>.+?)\s*\((?P<year>\d{4})\)\s+(?P<number>\d{2,4}(?:\.\d+)?)`),

	// "Batman #045.cbz"
	regexp.MustCompile(`(?i)^(?P<series>.+?)\s*#(?P<number>[\d.]+(?:\s*-\s*[\d.]+)?)`),

	// "Batman - Annual 01.cbz" or "Batman Annual 01.cbz"
	regexp.MustCompile(`(?i)^(?P<series>.+?)\s*[-:]\s*(?P<number>Annual\s+\d+)`),
	regexp.MustCompile(`(?i)^(?P<series>.+?)\s+(?P<number>Annual\s+\d+)`),

	// "Batman v2 012.cbr"
	regexp.MustCompile(`(?i)^(?P<series>.+?)\s+v(?P<volume>\d+)\s+(?P<number>[\d.]+)`),

	// "Batman 045.cbz"
	regexp.MustCompile(`(?i)^(?P<series>.+?)\s+(?P<number>\d{2,4}(?:\.\d+)?)\s*$`),

	// Fallback: everything before the extension is the series
	regexp.MustCompile(`(?i)^(?P<series>.+)$`),
}

// ParseFilename extracts series name, issue number, year, and volume from a comic filename.
func ParseFilename(filename string) ParsedFilename {
	// Strip the extension
	name := stripExtension(filename)

	for _, pat := range patterns {
		match := pat.FindStringSubmatch(name)
		if match == nil {
			continue
		}

		result := ParsedFilename{}
		for i, groupName := range pat.SubexpNames() {
			if i == 0 || groupName == "" {
				continue
			}
			val := strings.TrimSpace(match[i])
			if val == "" {
				continue
			}

			switch groupName {
			case "series":
				result.Series = cleanSeries(val)
			case "number":
				result.Number = cleanNumber(val)
			case "year":
				if y, err := strconv.Atoi(val); err == nil && y >= 1900 && y <= 2100 {
					result.Year = y
				}
			case "volume":
				if v, err := strconv.Atoi(val); err == nil {
					result.Volume = v
				}
			}
		}

		// Only accept if we got at least a series name
		if result.Series != "" {
			return result
		}
	}

	return ParsedFilename{Series: stripExtension(filename)}
}

// stripExtension removes the file extension.
func stripExtension(name string) string {
	for _, ext := range []string{".cbz", ".cbr", ".cb7", ".pdf", ".zip", ".rar"} {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			return name[:len(name)-len(ext)]
		}
	}
	return name
}

// cleanSeries normalizes a series name.
func cleanSeries(s string) string {
	// Remove trailing hyphens, dots, underscores
	s = strings.TrimRight(s, " -._")
	// Remove common group tags in parentheses at the end
	tagPattern := regexp.MustCompile(`\s*\([^)]*(?:scan|digital|empire|minutemen|DCP|noads|HD)\s*\)\s*$`)
	s = tagPattern.ReplaceAllString(s, "")
	s = strings.TrimSpace(s)
	return s
}

// cleanNumber normalizes an issue number.
func cleanNumber(s string) string {
	s = strings.TrimSpace(s)
	// Remove leading zeros for pure numeric issues
	if num, err := strconv.ParseFloat(s, 64); err == nil {
		if num == float64(int(num)) {
			return strconv.Itoa(int(num))
		}
		return strconv.FormatFloat(num, 'f', -1, 64)
	}
	// Handle "Annual 01" -> "Annual 1"
	annualPat := regexp.MustCompile(`(?i)^(Annual\s+)0*(\d+)$`)
	if m := annualPat.FindStringSubmatch(s); m != nil {
		return m[1] + m[2]
	}
	return s
}

// SortNumber converts an issue number string to a float for sorting.
func SortNumber(number string) float64 {
	if number == "" {
		return 0
	}
	// Handle "Annual X" style
	annualPat := regexp.MustCompile(`(?i)Annual\s+(\d+)`)
	if m := annualPat.FindStringSubmatch(number); m != nil {
		if n, err := strconv.ParseFloat(m[1], 64); err == nil {
			return 10000 + n // Sort annuals after regular issues
		}
	}
	// Try to parse as float
	if n, err := strconv.ParseFloat(number, 64); err == nil {
		return n
	}
	return 0
}

// MakeSortTitle creates a sort-friendly version of a title.
// Strips leading articles ("The", "A", "An") and lowercases.
func MakeSortTitle(title string) string {
	lower := strings.ToLower(strings.TrimSpace(title))
	for _, article := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(lower, article) {
			return strings.TrimPrefix(lower, article)
		}
	}
	return lower
}

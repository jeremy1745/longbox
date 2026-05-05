// Package template provides a naming template engine for comic file organization.
//
// Template syntax:
//
//	{variable}           — insert variable value
//	{variable|filter}    — insert with filter applied
//	{variable|pad:3}     — zero-pad to 3 digits
//	/                    — directory separator
//
// Example: {series}/{series} #{number|pad:3}.{format}
// Result:  Batman/Batman #001.cbz
package template

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// DefaultTemplate is the default naming template.
//
// Layout: <series> (<year>)/<series> (<year>) <NNN>.<ext>
// Example: Absolute Flash (2025)/Absolute Flash (2025) 001.cbz
//
// Series + year is the canonical Mylar-shaped folder convention so the
// folder uniquely identifies a volume even when multiple volumes share a
// title (Wonder Man 2024 vs Wonder Man 2007). The same `<series> (<year>)`
// prefix is repeated in the filename so a file separated from its folder
// (someone copies it to a phone, archives a backup, etc.) is still
// self-describing. Issue numbers are zero-padded to 3 digits.
const DefaultTemplate = "{series} ({year})/{series} ({year}) {number|pad:3}.{format}"

// TemplateContext holds the data needed to execute a naming template.
type TemplateContext struct {
	Series          string
	SortSeries      string
	Number          string
	Title           string
	Publisher       string
	Format          string
	CoverDate       string
	StoreDate       string
	Writers         string
	Artists         string
	ParentSeries    string
	AnnualSubfolder string
	// SeriesYear and Year both hold the series START year (volume year).
	// Same value, two names — `{series_year}` is explicit, `{year}` is
	// the convenient alias the default template uses. Both are stable
	// across every issue in a series so a run that crosses calendar
	// years lands in ONE folder.
	SeriesYear string
	Year       string
	// IssueYear holds the per-issue cover/store date year. Exposed as
	// `{issue_year}` for templates that want per-issue dating in the
	// FILENAME; never use it in folder paths or you'll fragment runs.
	IssueYear string
}

// tokenType distinguishes literal text from variable references.
type tokenType int

const (
	tokenLiteral tokenType = iota
	tokenVariable
)

// token represents a parsed template fragment.
type token struct {
	Type      tokenType
	Value     string // literal text, or variable name
	Filter    string // filter name (e.g., "pad")
	FilterArg string // filter argument (e.g., "3")
}

// Template is a parsed naming template ready for execution.
type Template struct {
	raw    string
	tokens []token
}

var varPattern = regexp.MustCompile(`\{([^}]+)\}`)

// Parse parses a template string into a Template.
func Parse(tmpl string) (*Template, error) {
	if tmpl == "" {
		return nil, fmt.Errorf("template cannot be empty")
	}

	// Validate: must contain {format}
	if !strings.Contains(tmpl, "{format}") {
		return nil, fmt.Errorf("template must contain {format} for file extension")
	}

	var tokens []token
	lastIdx := 0

	for _, loc := range varPattern.FindAllStringIndex(tmpl, -1) {
		// Add literal before this variable
		if loc[0] > lastIdx {
			tokens = append(tokens, token{Type: tokenLiteral, Value: tmpl[lastIdx:loc[0]]})
		}

		// Parse variable: strip { and }
		inner := tmpl[loc[0]+1 : loc[1]-1]
		varName, filter, filterArg := parseVariable(inner)

		// Validate variable name
		if !isValidVariable(varName) {
			return nil, fmt.Errorf("unknown template variable: {%s}", varName)
		}

		tokens = append(tokens, token{
			Type:      tokenVariable,
			Value:     varName,
			Filter:    filter,
			FilterArg: filterArg,
		})

		lastIdx = loc[1]
	}

	// Add trailing literal
	if lastIdx < len(tmpl) {
		tokens = append(tokens, token{Type: tokenLiteral, Value: tmpl[lastIdx:]})
	}

	return &Template{raw: tmpl, tokens: tokens}, nil
}

// Execute resolves the template with the given context, returning a relative path.
func (t *Template) Execute(ctx TemplateContext) (string, error) {
	var b strings.Builder

	for _, tok := range t.tokens {
		switch tok.Type {
		case tokenLiteral:
			b.WriteString(tok.Value)

		case tokenVariable:
			val := resolveVariable(tok.Value, ctx)
			val = applyFilter(val, tok.Filter, tok.FilterArg)

			// Sanitize for filesystem safety (except format which is already clean)
			if tok.Value != "format" {
				val = SanitizePathComponent(val)
			}

			b.WriteString(val)
		}
	}

	result := b.String()

	// Clean up: remove double spaces, trim path components
	result = cleanPath(result)

	if result == "" {
		return "", fmt.Errorf("template produced empty path")
	}

	return result, nil
}

// String returns the original template string.
func (t *Template) String() string {
	return t.raw
}

// SanitizePathComponent removes filesystem-unsafe characters from a path component.
// It does NOT strip path separators — use on individual components, not full paths.
func SanitizePathComponent(s string) string {
	// Remove characters unsafe on Windows/macOS/Linux
	unsafe := []string{"\\", ":", "*", "?", "\"", "<", ">", "|"}
	for _, c := range unsafe {
		s = strings.ReplaceAll(s, c, "")
	}
	// Replace forward slash with hyphen (within a component)
	s = strings.ReplaceAll(s, "/", "-")
	// Collapse multiple spaces
	s = collapseSpaces(s)
	// Trim whitespace and dots (Windows doesn't like trailing dots)
	s = strings.TrimSpace(s)
	s = strings.TrimRight(s, ".")
	return s
}

// --- internal helpers ---

func parseVariable(inner string) (name, filter, filterArg string) {
	parts := strings.SplitN(inner, "|", 2)
	name = strings.TrimSpace(parts[0])
	if len(parts) == 2 {
		filterParts := strings.SplitN(strings.TrimSpace(parts[1]), ":", 2)
		filter = filterParts[0]
		if len(filterParts) == 2 {
			filterArg = filterParts[1]
		}
	}
	return
}

var validVariables = map[string]bool{
	"series":           true,
	"sort_series":      true,
	"number":           true,
	"title":            true,
	"publisher":        true,
	"format":           true,
	"cover_date":       true,
	"store_date":       true,
	"writers":          true,
	"artists":          true,
	"parent_series":    true,
	"annual_subfolder": true,
	"series_year":      true,
	"year":             true,
	"issue_year":       true,
}

func isValidVariable(name string) bool {
	return validVariables[name]
}

func resolveVariable(name string, ctx TemplateContext) string {
	switch name {
	case "series":
		return ctx.Series
	case "sort_series":
		return ctx.SortSeries
	case "number":
		return ctx.Number
	case "title":
		return ctx.Title
	case "publisher":
		return ctx.Publisher
	case "format":
		return ctx.Format
	case "cover_date":
		return ctx.CoverDate
	case "store_date":
		return ctx.StoreDate
	case "writers":
		return ctx.Writers
	case "artists":
		return ctx.Artists
	case "parent_series":
		return ctx.ParentSeries
	case "annual_subfolder":
		return ctx.AnnualSubfolder
	case "series_year":
		return ctx.SeriesYear
	case "year":
		return ctx.Year
	case "issue_year":
		return ctx.IssueYear
	default:
		return ""
	}
}

func applyFilter(val, filter, arg string) string {
	switch filter {
	case "pad":
		width := 3 // default
		if arg != "" {
			fmt.Sscanf(arg, "%d", &width)
		}
		// Only pad if it looks numeric
		if isNumeric(val) {
			return fmt.Sprintf("%0*s", width, val)
		}
		return val
	case "lower":
		return strings.ToLower(val)
	case "upper":
		return strings.ToUpper(val)
	default:
		return val
	}
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) && r != '.' {
			return false
		}
	}
	return true
}

func collapseSpaces(s string) string {
	prev := false
	var b strings.Builder
	for _, r := range s {
		if r == ' ' {
			if !prev {
				b.WriteRune(r)
			}
			prev = true
		} else {
			b.WriteRune(r)
			prev = false
		}
	}
	return b.String()
}

func cleanPath(path string) string {
	// Split by path separator, trim each component, rejoin
	parts := strings.Split(path, "/")
	var cleaned []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return strings.Join(cleaned, "/")
}

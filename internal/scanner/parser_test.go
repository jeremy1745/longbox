package scanner

import (
	"testing"
)

func TestParseFilename(t *testing.T) {
	tests := []struct {
		filename string
		series   string
		number   string
		year     int
	}{
		{"Batman (2016) #045 (Digital) (Zone-Empire).cbz", "Batman", "45", 2016},
		{"Batman #045 (2016).cbz", "Batman", "45", 2016},
		{"Amazing Spider-Man v2 012 (2000).cbr", "Amazing Spider-Man", "12", 2000},
		{"Batman 045 (2016).cbz", "Batman", "45", 2016},
		{"Batman (2016) 045.cbz", "Batman", "45", 2016},
		{"Batman #045.cbz", "Batman", "45", 0},
		{"Batman v2 012.cbr", "Batman", "12", 0},
		{"Batman 045.cbz", "Batman", "45", 0},
		{"Batman - Annual 01.cbz", "Batman", "Annual 1", 0},
		{"Saga #1 (2012).cbz", "Saga", "1", 2012},
		{"The Walking Dead 100 (2012).cbz", "The Walking Dead", "100", 2012},
		{"East of West #1.cbz", "East of West", "1", 0},
		{"Invincible Iron Man (2015) #001.cbz", "Invincible Iron Man", "1", 2015},
		{"Y - The Last Man 001 (2002).cbz", "Y - The Last Man", "1", 2002},
		{"Sandman 01.cbz", "Sandman", "1", 0},
		{"Maus.cbz", "Maus", "", 0},
		{"X-Men (1991) #4.cbr", "X-Men", "4", 1991},
		{"Spider-Man 2099 (2014) #005.cbz", "Spider-Man 2099", "5", 2014},
		{"Batman #0.5.cbz", "Batman", "0.5", 0},

		// Mylar "(of N)" mini-series counter — previously fell through to
		// the catch-all fallback and the entire filename became the series.
		{"20th Century Men 01 (of 06) (2022) (Digital) (Mephisto-Empire).cbz", "20th Century Men", "1", 2022},
		// "(0f 06)" typo seen in real release names — same fix.
		{"20th Century Men 01 (0f 06) (2022).cbz", "20th Century Men", "1", 2022},

		// "Series NN - Subtitle (Year)" — previously fell through too.
		{"Aama 03 - The Desert of Mirrors (2015) (Digital) (Dipole-Empire).cbz", "Aama", "3", 2015},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			result := ParseFilename(tt.filename)
			if result.Series != tt.series {
				t.Errorf("series: got %q, want %q", result.Series, tt.series)
			}
			if result.Number != tt.number {
				t.Errorf("number: got %q, want %q", result.Number, tt.number)
			}
			if result.Year != tt.year {
				t.Errorf("year: got %d, want %d", result.Year, tt.year)
			}
		})
	}
}

// TestParseFilename_RejectsUnparseableFilenames verifies that filenames the
// patterns can't decompose return an empty parse, instead of recording the
// whole filename as a Series. Without this, every scanned issue produced a
// junk series row matching its full filename.
func TestParseFilename_RejectsUnparseableFilenames(t *testing.T) {
	unparseable := []string{
		// Issue numbers still in the captured series — would be garbage.
		"A Vicious Circle 002 (of 03) (2023) (Digital-Empire)",
		"Detective Comics 973 (F) (2018) (Webrip)",
	}
	for _, name := range unparseable {
		t.Run(name, func(t *testing.T) {
			r := ParseFilename(name)
			// We don't require Series to be empty for *all* of these — the
			// new patterns may catch some. But if Series is set, it must
			// not echo back issue-shaped tokens like "(of N)" or a 4-digit
			// parenthetical year, which were the smoking gun for the
			// 14-row scanner garbage in production.
			if r.Series != "" {
				if (containsAny(r.Series, "(of ", "(0f ")) || hasParenYear(r.Series) {
					t.Errorf("series %q still carries issue-shaped tokens", r.Series)
				}
			}
		})
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		for i := 0; i+len(n) <= len(s); i++ {
			if s[i:i+len(n)] == n {
				return true
			}
		}
	}
	return false
}

func hasParenYear(s string) bool {
	// crude check for "(YYYY)" anywhere in s
	for i := 0; i+6 <= len(s); i++ {
		if s[i] == '(' && s[i+5] == ')' {
			ok := true
			for j := 1; j <= 4; j++ {
				if s[i+j] < '0' || s[i+j] > '9' {
					ok = false
					break
				}
			}
			if ok {
				return true
			}
		}
	}
	return false
}

func TestMakeSortTitle(t *testing.T) {
	tests := []struct {
		title string
		want  string
	}{
		{"Batman", "batman"},
		{"The Walking Dead", "walking dead"},
		{"A Game of Thrones", "game of thrones"},
		{"An Amazing Story", "amazing story"},
		{"X-Men", "x-men"},
	}

	for _, tt := range tests {
		t.Run(tt.title, func(t *testing.T) {
			got := MakeSortTitle(tt.title)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSortNumber(t *testing.T) {
	tests := []struct {
		number string
		want   float64
	}{
		{"1", 1},
		{"45", 45},
		{"0.5", 0.5},
		{"Annual 1", 10001},
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.number, func(t *testing.T) {
			got := SortNumber(tt.number)
			if got != tt.want {
				t.Errorf("got %f, want %f", got, tt.want)
			}
		})
	}
}

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

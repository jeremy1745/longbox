package archive

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// ComicInfo represents the metadata from a ComicInfo.xml file.
// See: https://anansi-project.github.io/docs/comicinfo/documentation
type ComicInfo struct {
	XMLName     xml.Name `xml:"ComicInfo"`
	Title       string   `xml:"Title,omitempty"`
	Series      string   `xml:"Series,omitempty"`
	Number      string   `xml:"Number,omitempty"`
	Volume      int      `xml:"Volume,omitempty"`
	Summary     string   `xml:"Summary,omitempty"`
	Year        int      `xml:"Year,omitempty"`
	Month       int      `xml:"Month,omitempty"`
	Day         int      `xml:"Day,omitempty"`
	Writer      string   `xml:"Writer,omitempty"`
	Penciller   string   `xml:"Penciller,omitempty"`
	Inker       string   `xml:"Inker,omitempty"`
	Colorist    string   `xml:"Colorist,omitempty"`
	Letterer    string   `xml:"Letterer,omitempty"`
	CoverArtist string   `xml:"CoverArtist,omitempty"`
	Editor      string   `xml:"Editor,omitempty"`
	Publisher   string   `xml:"Publisher,omitempty"`
	Genre       string   `xml:"Genre,omitempty"`
	PageCount   int      `xml:"PageCount,omitempty"`
	StoryArc    string   `xml:"StoryArc,omitempty"`
	SeriesGroup string   `xml:"SeriesGroup,omitempty"`
	Count       int      `xml:"Count,omitempty"` // Total issue count for the series
	Web         string   `xml:"Web,omitempty"`
}

// ParseComicInfo parses a ComicInfo.xml from a reader.
func ParseComicInfo(r io.Reader) (*ComicInfo, error) {
	var info ComicInfo
	decoder := xml.NewDecoder(r)
	if err := decoder.Decode(&info); err != nil {
		return nil, fmt.Errorf("parsing ComicInfo.xml: %w", err)
	}
	return &info, nil
}

// Writers returns a comma-separated list of all writers.
func (ci *ComicInfo) Writers() string {
	return ci.Writer
}

// Artists returns a comma-separated list of all artists (penciller, inker, colorist, cover artist).
func (ci *ComicInfo) Artists() string {
	var artists []string
	for _, a := range []string{ci.Penciller, ci.Inker, ci.Colorist, ci.CoverArtist} {
		if a != "" {
			artists = append(artists, a)
		}
	}
	return strings.Join(artists, ", ")
}

// ReadComicInfo attempts to find and parse ComicInfo.xml from an archive.
// Returns nil if no ComicInfo.xml is found.
func ReadComicInfo(a Archive) (*ComicInfo, error) {
	entries, err := a.ListEntries()
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if strings.EqualFold(entry.Name, "ComicInfo.xml") ||
			strings.HasSuffix(strings.ToLower(entry.Name), "/comicinfo.xml") {
			rc, err := a.ExtractFile(entry.Name)
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return ParseComicInfo(rc)
		}
	}

	return nil, nil // No ComicInfo.xml found
}

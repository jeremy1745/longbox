package newznab

import (
	"encoding/xml"
	"time"
)

// Response wraps the Newznab RSS XML response.
type Response struct {
	XMLName xml.Name `xml:"rss"`
	Channel Channel  `xml:"channel"`
}

// Channel contains the search results.
type Channel struct {
	Title    string   `xml:"title"`
	Response RespAttr `xml:"http://www.newznab.com/DTD/2010/feeds/attributes/ response"`
	Items    []Item   `xml:"item"`
}

// RespAttr contains pagination info from the Newznab response.
type RespAttr struct {
	Offset int `xml:"offset,attr"`
	Total  int `xml:"total,attr"`
}

// Item represents a single NZB in the search results.
type Item struct {
	Title       string `xml:"title"`
	GUID        GUID   `xml:"guid"`
	Link        string `xml:"link"`
	PubDate     string `xml:"pubDate"`
	Category    string `xml:"category"`
	Description string `xml:"description"`
	Size        int64  `xml:"size"`
	Attributes  []Attr `xml:"http://www.newznab.com/DTD/2010/feeds/attributes/ attr"`
}

// GUID holds the NZB identifier.
type GUID struct {
	Value       string `xml:",chardata"`
	IsPermaLink string `xml:"isPermaLink,attr"`
}

// Attr is a Newznab extended attribute on an item.
type Attr struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

// SearchResult is the parsed, normalized result from a Newznab search.
type SearchResult struct {
	Title       string    `json:"title"`
	NZBURL      string    `json:"nzb_url"`
	Size        int64     `json:"size"`
	PublishDate time.Time `json:"publish_date"`
	Category    string    `json:"category"`
	Grabs       int       `json:"grabs"`
	GUID        string    `json:"guid"`
	InfoURL     string    `json:"info_url,omitempty"`
	IndexerName string    `json:"indexer_name"`
	IndexerID   int64     `json:"indexer_id"`
}

// prowlarrSearchResult maps the JSON response from Prowlarr's native /api/v1/search endpoint.
type prowlarrSearchResult struct {
	GUID        string             `json:"guid"`
	Title       string             `json:"title"`
	Size        int64              `json:"size"`
	Grabs       int                `json:"grabs"`
	PublishDate string             `json:"publishDate"`
	DownloadURL string             `json:"downloadUrl"`
	InfoURL     string             `json:"infoUrl"`
	IndexerID   int                `json:"indexerId"`
	Indexer     string             `json:"indexer"`
	Categories  []prowlarrCategory `json:"categories"`
}

type prowlarrCategory struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// CapsResponse wraps the Newznab capabilities XML response (used for testing connections).
type CapsResponse struct {
	XMLName xml.Name   `xml:"caps"`
	Server  CapsServer `xml:"server"`
}

// CapsServer holds server info from the caps endpoint.
type CapsServer struct {
	Title   string `xml:"title,attr"`
	Version string `xml:"version,attr"`
}

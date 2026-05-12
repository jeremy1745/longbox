package model

import "time"

// IndexerType identifies the kind of Usenet indexer.
type IndexerType string

const (
	IndexerTypeNewznab  IndexerType = "newznab"
	IndexerTypeHydra2   IndexerType = "nzbhydra2"
	IndexerTypeProwlarr IndexerType = "prowlarr"
)

// Indexer represents a configured Usenet indexer.
type Indexer struct {
	ID         int64       `json:"id"`
	Name       string      `json:"name"`
	URL        string      `json:"url"`
	APIKey     string      `json:"api_key,omitempty"`
	Type       IndexerType `json:"type"`
	Priority   int         `json:"priority"`
	Enabled    bool        `json:"enabled"`
	Categories string      `json:"categories"`
	CreatedAt  time.Time   `json:"created_at"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// MaskAPIKey returns the API key with only the first and last 4 characters visible.
func (idx *Indexer) MaskAPIKey() string {
	if idx.APIKey == "" {
		return ""
	}
	if len(idx.APIKey) <= 8 {
		return "****"
	}
	return idx.APIKey[:4] + "..." + idx.APIKey[len(idx.APIKey)-4:]
}

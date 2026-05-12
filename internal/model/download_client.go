package model

import "time"

// DownloadClientType identifies the kind of download client.
type DownloadClientType string

const (
	DownloadClientTypeSABnzbd DownloadClientType = "sabnzbd"
)

// DownloadClient represents a configured download client (e.g. SABnzbd).
type DownloadClient struct {
	ID        int64              `json:"id"`
	Name      string             `json:"name"`
	Type      DownloadClientType `json:"type"`
	URL       string             `json:"url"`
	APIKey    string             `json:"api_key,omitempty"`
	Category  string             `json:"category"`
	Priority  int                `json:"priority"`
	Enabled   bool               `json:"enabled"`
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
}

// MaskAPIKey returns the API key with only the first and last 4 characters visible.
func (dc *DownloadClient) MaskAPIKey() string {
	if dc.APIKey == "" {
		return ""
	}
	if len(dc.APIKey) <= 8 {
		return "****"
	}
	return dc.APIKey[:4] + "..." + dc.APIKey[len(dc.APIKey)-4:]
}

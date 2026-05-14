package model

// BlocklistEntry represents a blocked NZB GUID that should not be re-downloaded.
type BlocklistEntry struct {
	ID        int64  `json:"id"`
	NZBGuid   string `json:"nzb_guid"`
	NZBName   string `json:"nzb_name"`
	Reason    string `json:"reason"`
	BlockedAt string `json:"blocked_at"`
}

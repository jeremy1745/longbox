package repository

import (
	"database/sql"
	"fmt"
	"strings"
)

type Publisher struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	ComicVineID *int64 `json:"comicvine_id,omitempty"`
}

type PublisherRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewPublisherRepo(read, write *sql.DB) *PublisherRepo {
	return &PublisherRepo{read: read, write: write}
}

// publisherAliases maps abbreviated / scene-style publisher names to their
// canonical industry-standard form. Keyed by lowercase name. Applied by
// FindOrCreateByName before any lookup or insert, so future ComicInfo-fed
// publisher writes can't reintroduce the duplicates that migration 013
// just cleaned up.
//
// Keep imprint specifiers ("Image - Skybound", "Image - Top Cow") distinct
// — they're meaningful at the catalog level.
var publisherAliases = map[string]string{
	"dc":         "DC Comics",
	"marvel":     "Marvel Comics",
	"image":      "Image Comics",
	"dark horse": "Dark Horse Comics",
	"idw":        "IDW Publishing",
	"titan":      "Titan Comics",
}

// normalizePublisher returns the canonical spelling for an input name.
// Trims surrounding whitespace and consults publisherAliases; if no alias
// matches, returns the trimmed original.
func normalizePublisher(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if canonical, ok := publisherAliases[strings.ToLower(name)]; ok {
		return canonical
	}
	return name
}

// FindOrCreateByName finds a publisher by name, or creates one if it doesn't exist.
// Applies normalizePublisher first so scene-style aliases ("DC" → "DC Comics",
// "Marvel" → "Marvel Comics", etc.) coalesce to a single canonical row.
func (r *PublisherRepo) FindOrCreateByName(name string, cvID *int64) (*Publisher, error) {
	name = normalizePublisher(name)
	if name == "" {
		return nil, nil
	}

	// Try to find by name first
	p := &Publisher{}
	err := r.read.QueryRow(`SELECT id, name, comicvine_id FROM publishers WHERE LOWER(name) = LOWER(?)`, name).
		Scan(&p.ID, &p.Name, &p.ComicVineID)
	if err == nil {
		return p, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("finding publisher: %w", err)
	}

	// Create new publisher
	res, err := r.write.Exec(`INSERT INTO publishers (name, comicvine_id) VALUES (?, ?)`, name, cvID)
	if err != nil {
		return nil, fmt.Errorf("creating publisher: %w", err)
	}
	id, _ := res.LastInsertId()
	return &Publisher{ID: id, Name: name, ComicVineID: cvID}, nil
}

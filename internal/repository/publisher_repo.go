package repository

import (
	"database/sql"
	"fmt"
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

// FindOrCreateByName finds a publisher by name, or creates one if it doesn't exist.
func (r *PublisherRepo) FindOrCreateByName(name string, cvID *int64) (*Publisher, error) {
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

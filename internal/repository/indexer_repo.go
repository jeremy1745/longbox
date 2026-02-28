package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jeremy/longbox/internal/model"
)

type IndexerRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewIndexerRepo(read, write *sql.DB) *IndexerRepo {
	return &IndexerRepo{read: read, write: write}
}

func (r *IndexerRepo) Create(idx *model.Indexer) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.write.Exec(`
		INSERT INTO indexers (name, url, api_key, type, priority, enabled, categories, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		idx.Name, idx.URL, idx.APIKey, idx.Type, idx.Priority, idx.Enabled, idx.Categories, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting indexer: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting last insert id: %w", err)
	}
	idx.ID = id
	return nil
}

func (r *IndexerRepo) GetByID(id int64) (*model.Indexer, error) {
	row := r.read.QueryRow(`SELECT id, name, url, api_key, type, priority, enabled, categories,
		created_at, updated_at FROM indexers WHERE id = ?`, id)
	return scanIndexer(row)
}

func (r *IndexerRepo) Update(idx *model.Indexer) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.write.Exec(`
		UPDATE indexers SET name = ?, url = ?, api_key = ?, type = ?, priority = ?,
		enabled = ?, categories = ?, updated_at = ? WHERE id = ?`,
		idx.Name, idx.URL, idx.APIKey, idx.Type, idx.Priority, idx.Enabled, idx.Categories, now, idx.ID,
	)
	return err
}

func (r *IndexerRepo) Delete(id int64) error {
	_, err := r.write.Exec(`DELETE FROM indexers WHERE id = ?`, id)
	return err
}

func (r *IndexerRepo) List() ([]model.Indexer, error) {
	rows, err := r.read.Query(`SELECT id, name, url, api_key, type, priority, enabled, categories,
		created_at, updated_at FROM indexers ORDER BY priority ASC, name ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing indexers: %w", err)
	}
	defer rows.Close()

	var indexers []model.Indexer
	for rows.Next() {
		idx, err := scanIndexerRow(rows)
		if err != nil {
			return nil, err
		}
		indexers = append(indexers, *idx)
	}
	return indexers, nil
}

func (r *IndexerRepo) ListEnabled() ([]model.Indexer, error) {
	rows, err := r.read.Query(`SELECT id, name, url, api_key, type, priority, enabled, categories,
		created_at, updated_at FROM indexers WHERE enabled = 1 ORDER BY priority ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing enabled indexers: %w", err)
	}
	defer rows.Close()

	var indexers []model.Indexer
	for rows.Next() {
		idx, err := scanIndexerRow(rows)
		if err != nil {
			return nil, err
		}
		indexers = append(indexers, *idx)
	}
	return indexers, nil
}

func scanIndexer(row *sql.Row) (*model.Indexer, error) {
	idx := &model.Indexer{}
	var createdAt, updatedAt string
	err := row.Scan(
		&idx.ID, &idx.Name, &idx.URL, &idx.APIKey, &idx.Type, &idx.Priority,
		&idx.Enabled, &idx.Categories, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning indexer: %w", err)
	}
	idx.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	idx.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return idx, nil
}

func scanIndexerRow(rows *sql.Rows) (*model.Indexer, error) {
	idx := &model.Indexer{}
	var createdAt, updatedAt string
	err := rows.Scan(
		&idx.ID, &idx.Name, &idx.URL, &idx.APIKey, &idx.Type, &idx.Priority,
		&idx.Enabled, &idx.Categories, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning indexer row: %w", err)
	}
	idx.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	idx.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return idx, nil
}

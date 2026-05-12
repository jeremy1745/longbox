package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jeremy/longbox/internal/model"
)

type DownloadClientRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewDownloadClientRepo(read, write *sql.DB) *DownloadClientRepo {
	return &DownloadClientRepo{read: read, write: write}
}

func (r *DownloadClientRepo) Create(dc *model.DownloadClient) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.write.Exec(`
		INSERT INTO download_clients (name, type, url, api_key, category, priority, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		dc.Name, dc.Type, dc.URL, dc.APIKey, dc.Category, dc.Priority, dc.Enabled, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting download client: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting last insert id: %w", err)
	}
	dc.ID = id
	return nil
}

func (r *DownloadClientRepo) GetByID(id int64) (*model.DownloadClient, error) {
	row := r.read.QueryRow(`SELECT id, name, type, url, api_key, category, priority, enabled,
		created_at, updated_at FROM download_clients WHERE id = ?`, id)
	return scanDownloadClient(row)
}

func (r *DownloadClientRepo) Update(dc *model.DownloadClient) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.write.Exec(`
		UPDATE download_clients SET name = ?, type = ?, url = ?, api_key = ?, category = ?,
		priority = ?, enabled = ?, updated_at = ? WHERE id = ?`,
		dc.Name, dc.Type, dc.URL, dc.APIKey, dc.Category, dc.Priority, dc.Enabled, now, dc.ID,
	)
	return err
}

func (r *DownloadClientRepo) Delete(id int64) error {
	_, err := r.write.Exec(`DELETE FROM download_clients WHERE id = ?`, id)
	return err
}

func (r *DownloadClientRepo) List() ([]model.DownloadClient, error) {
	rows, err := r.read.Query(`SELECT id, name, type, url, api_key, category, priority, enabled,
		created_at, updated_at FROM download_clients ORDER BY priority ASC, name ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing download clients: %w", err)
	}
	defer rows.Close()

	var clients []model.DownloadClient
	for rows.Next() {
		dc, err := scanDownloadClientRow(rows)
		if err != nil {
			return nil, err
		}
		clients = append(clients, *dc)
	}
	return clients, nil
}

func (r *DownloadClientRepo) GetFirstEnabled() (*model.DownloadClient, error) {
	row := r.read.QueryRow(`SELECT id, name, type, url, api_key, category, priority, enabled,
		created_at, updated_at FROM download_clients WHERE enabled = 1 ORDER BY priority ASC LIMIT 1`)
	return scanDownloadClient(row)
}

func scanDownloadClient(row *sql.Row) (*model.DownloadClient, error) {
	dc := &model.DownloadClient{}
	var createdAt, updatedAt string
	err := row.Scan(
		&dc.ID, &dc.Name, &dc.Type, &dc.URL, &dc.APIKey, &dc.Category,
		&dc.Priority, &dc.Enabled, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning download client: %w", err)
	}
	dc.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	dc.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return dc, nil
}

func scanDownloadClientRow(rows *sql.Rows) (*model.DownloadClient, error) {
	dc := &model.DownloadClient{}
	var createdAt, updatedAt string
	err := rows.Scan(
		&dc.ID, &dc.Name, &dc.Type, &dc.URL, &dc.APIKey, &dc.Category,
		&dc.Priority, &dc.Enabled, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning download client row: %w", err)
	}
	dc.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	dc.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return dc, nil
}

package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jeremy/longbox/internal/model"
)

type DownloadHistoryRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewDownloadHistoryRepo(read, write *sql.DB) *DownloadHistoryRepo {
	return &DownloadHistoryRepo{read: read, write: write}
}

func (r *DownloadHistoryRepo) Create(item *model.DownloadHistoryItem) error {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := r.write.Exec(`
		INSERT INTO download_history (issue_id, indexer_id, download_client_id, nzb_name, nzb_url,
			external_id, status, size, message, grabbed_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.IssueID, item.IndexerID, item.DownloadClientID, item.NZBName, item.NZBURL,
		item.ExternalID, item.Status, item.Size, item.Message, now, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting download history: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting last insert id: %w", err)
	}
	item.ID = id
	item.GrabbedAt = now
	return nil
}

func (r *DownloadHistoryRepo) GetByID(id int64) (*model.DownloadHistoryItem, error) {
	row := r.read.QueryRow(`
		SELECT dh.id, dh.issue_id, dh.indexer_id, dh.download_client_id, dh.nzb_name, dh.nzb_url,
			dh.external_id, dh.status, dh.size, dh.message, dh.grabbed_at, dh.completed_at,
			dh.created_at, dh.updated_at,
			COALESCE(s.title, ''), COALESCE(i.issue_number, ''), COALESCE(idx.name, '')
		FROM download_history dh
		LEFT JOIN issues i ON dh.issue_id = i.id
		LEFT JOIN series s ON i.series_id = s.id
		LEFT JOIN indexers idx ON dh.indexer_id = idx.id
		WHERE dh.id = ?`, id)
	return scanDownloadHistory(row)
}

func (r *DownloadHistoryRepo) GetByExternalID(externalID string) (*model.DownloadHistoryItem, error) {
	row := r.read.QueryRow(`
		SELECT dh.id, dh.issue_id, dh.indexer_id, dh.download_client_id, dh.nzb_name, dh.nzb_url,
			dh.external_id, dh.status, dh.size, dh.message, dh.grabbed_at, dh.completed_at,
			dh.created_at, dh.updated_at,
			COALESCE(s.title, ''), COALESCE(i.issue_number, ''), COALESCE(idx.name, '')
		FROM download_history dh
		LEFT JOIN issues i ON dh.issue_id = i.id
		LEFT JOIN series s ON i.series_id = s.id
		LEFT JOIN indexers idx ON dh.indexer_id = idx.id
		WHERE dh.external_id = ?`, externalID)
	return scanDownloadHistory(row)
}

func (r *DownloadHistoryRepo) UpdateStatus(id int64, status model.DownloadStatus, message string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	var completedAt *string
	if status == model.DownloadStatusCompleted || status == model.DownloadStatusFailed || status == model.DownloadStatusImportFailed {
		completedAt = &now
	}
	_, err := r.write.Exec(`
		UPDATE download_history SET status = ?, message = ?, completed_at = ?, updated_at = ? WHERE id = ?`,
		status, message, completedAt, now, id,
	)
	return err
}

func (r *DownloadHistoryRepo) SetExternalID(id int64, externalID string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := r.write.Exec(`UPDATE download_history SET external_id = ?, updated_at = ? WHERE id = ?`,
		externalID, now, id)
	return err
}

func (r *DownloadHistoryRepo) List(page, perPage int) ([]model.DownloadHistoryItem, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	}
	offset := (page - 1) * perPage

	var total int
	if err := r.read.QueryRow(`SELECT COUNT(*) FROM download_history`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting download history: %w", err)
	}

	rows, err := r.read.Query(`
		SELECT dh.id, dh.issue_id, dh.indexer_id, dh.download_client_id, dh.nzb_name, dh.nzb_url,
			dh.external_id, dh.status, dh.size, dh.message, dh.grabbed_at, dh.completed_at,
			dh.created_at, dh.updated_at,
			COALESCE(s.title, ''), COALESCE(i.issue_number, ''), COALESCE(idx.name, '')
		FROM download_history dh
		LEFT JOIN issues i ON dh.issue_id = i.id
		LEFT JOIN series s ON i.series_id = s.id
		LEFT JOIN indexers idx ON dh.indexer_id = idx.id
		ORDER BY dh.id DESC
		LIMIT ? OFFSET ?`, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing download history: %w", err)
	}
	defer rows.Close()

	var items []model.DownloadHistoryItem
	for rows.Next() {
		item, err := scanDownloadHistoryRow(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	return items, total, nil
}

func (r *DownloadHistoryRepo) ListPending() ([]model.DownloadHistoryItem, error) {
	rows, err := r.read.Query(`
		SELECT dh.id, dh.issue_id, dh.indexer_id, dh.download_client_id, dh.nzb_name, dh.nzb_url,
			dh.external_id, dh.status, dh.size, dh.message, dh.grabbed_at, dh.completed_at,
			dh.created_at, dh.updated_at,
			COALESCE(s.title, ''), COALESCE(i.issue_number, ''), COALESCE(idx.name, '')
		FROM download_history dh
		LEFT JOIN issues i ON dh.issue_id = i.id
		LEFT JOIN series s ON i.series_id = s.id
		LEFT JOIN indexers idx ON dh.indexer_id = idx.id
		WHERE dh.status IN ('grabbed', 'downloading')
		ORDER BY dh.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing pending downloads: %w", err)
	}
	defer rows.Close()

	var items []model.DownloadHistoryItem
	for rows.Next() {
		item, err := scanDownloadHistoryRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, nil
}

func (r *DownloadHistoryRepo) ExistsForIssue(issueID int64) (bool, error) {
	var count int
	err := r.read.QueryRow(`
		SELECT COUNT(*) FROM download_history
		WHERE issue_id = ? AND status IN ('grabbed', 'downloading', 'completed')`, issueID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking download existence: %w", err)
	}
	return count > 0, nil
}

func scanDownloadHistory(row *sql.Row) (*model.DownloadHistoryItem, error) {
	item := &model.DownloadHistoryItem{}
	var createdAt, updatedAt string
	err := row.Scan(
		&item.ID, &item.IssueID, &item.IndexerID, &item.DownloadClientID,
		&item.NZBName, &item.NZBURL, &item.ExternalID, &item.Status,
		&item.Size, &item.Message, &item.GrabbedAt, &item.CompletedAt,
		&createdAt, &updatedAt,
		&item.SeriesTitle, &item.IssueNumber, &item.IndexerName,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning download history: %w", err)
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return item, nil
}

func scanDownloadHistoryRow(rows *sql.Rows) (*model.DownloadHistoryItem, error) {
	item := &model.DownloadHistoryItem{}
	var createdAt, updatedAt string
	err := rows.Scan(
		&item.ID, &item.IssueID, &item.IndexerID, &item.DownloadClientID,
		&item.NZBName, &item.NZBURL, &item.ExternalID, &item.Status,
		&item.Size, &item.Message, &item.GrabbedAt, &item.CompletedAt,
		&createdAt, &updatedAt,
		&item.SeriesTitle, &item.IssueNumber, &item.IndexerName,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning download history row: %w", err)
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	item.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return item, nil
}

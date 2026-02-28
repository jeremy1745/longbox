package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jeremy/longbox/internal/model"
)

type IssueRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewIssueRepo(read, write *sql.DB) *IssueRepo {
	return &IssueRepo{read: read, write: write}
}

func (r *IssueRepo) Create(issue *model.Issue) error {
	res, err := r.write.Exec(`
		INSERT INTO issues (series_id, issue_number, sort_number, title, comicvine_id,
			description, cover_date, store_date, cover_url, writers, artists, read_status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		issue.SeriesID, issue.IssueNumber, issue.SortNumber, issue.Title, issue.ComicVineID,
		issue.Description, issue.CoverDate, issue.StoreDate, issue.CoverURL,
		issue.Writers, issue.Artists, issue.ReadStatus,
	)
	if err != nil {
		return fmt.Errorf("inserting issue: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting last insert id: %w", err)
	}
	issue.ID = id
	return nil
}

func (r *IssueRepo) GetByID(id int64) (*model.Issue, error) {
	row := r.read.QueryRow(`
		SELECT i.id, i.series_id, i.issue_number, i.sort_number, COALESCE(i.title,''), i.comicvine_id,
			COALESCE(i.description,''), COALESCE(i.cover_date,''), COALESCE(i.store_date,''), COALESCE(i.cover_url,''), COALESCE(i.writers,''), COALESCE(i.artists,''),
			i.read_status, i.rating, i.last_read_page, i.metadata_locked, i.created_at, i.updated_at,
			CASE WHEN cf.id IS NOT NULL THEN 1 ELSE 0 END as has_file,
			cf.id as file_id,
			s.title as series_title
		FROM issues i
		LEFT JOIN comic_files cf ON cf.issue_id = i.id
		LEFT JOIN series s ON i.series_id = s.id
		WHERE i.id = ?`, id)
	return scanIssue(row)
}

// FindBySeriesAndNumber finds an issue by series ID and issue number.
func (r *IssueRepo) FindBySeriesAndNumber(seriesID int64, number string) (*model.Issue, error) {
	row := r.read.QueryRow(`
		SELECT i.id, i.series_id, i.issue_number, i.sort_number, COALESCE(i.title,''), i.comicvine_id,
			COALESCE(i.description,''), COALESCE(i.cover_date,''), COALESCE(i.store_date,''), COALESCE(i.cover_url,''), COALESCE(i.writers,''), COALESCE(i.artists,''),
			i.read_status, i.rating, i.last_read_page, i.metadata_locked, i.created_at, i.updated_at,
			0 as has_file, NULL as file_id, '' as series_title
		FROM issues i
		WHERE i.series_id = ? AND i.issue_number = ?`, seriesID, number)
	return scanIssue(row)
}

// ListBySeries returns all issues for a given series, sorted by sort_number.
func (r *IssueRepo) ListBySeries(seriesID int64) ([]model.Issue, error) {
	rows, err := r.read.Query(`
		SELECT i.id, i.series_id, i.issue_number, i.sort_number, COALESCE(i.title,''), i.comicvine_id,
			COALESCE(i.description,''), COALESCE(i.cover_date,''), COALESCE(i.store_date,''), COALESCE(i.cover_url,''), COALESCE(i.writers,''), COALESCE(i.artists,''),
			i.read_status, i.rating, i.last_read_page, i.metadata_locked, i.created_at, i.updated_at,
			CASE WHEN cf.id IS NOT NULL THEN 1 ELSE 0 END as has_file,
			cf.id as file_id,
			s.title as series_title
		FROM issues i
		LEFT JOIN comic_files cf ON cf.issue_id = i.id
		LEFT JOIN series s ON i.series_id = s.id
		WHERE i.series_id = ?
		ORDER BY i.sort_number ASC`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("listing issues: %w", err)
	}
	defer rows.Close()

	var issues []model.Issue
	for rows.Next() {
		issue, err := scanIssueRow(rows)
		if err != nil {
			return nil, err
		}
		issues = append(issues, *issue)
	}
	return issues, nil
}

func (r *IssueRepo) Update(issue *model.Issue) error {
	_, err := r.write.Exec(`
		UPDATE issues SET title = ?, description = ?, cover_date = ?, store_date = ?,
			writers = ?, artists = ?, read_status = ?, updated_at = ?
		WHERE id = ?`,
		issue.Title, issue.Description, issue.CoverDate, issue.StoreDate,
		issue.Writers, issue.Artists, issue.ReadStatus,
		time.Now().UTC().Format(time.RFC3339), issue.ID,
	)
	return err
}

// UpdateFromMetadata updates an issue with ComicVine metadata.
func (r *IssueRepo) UpdateFromMetadata(issue *model.Issue) error {
	_, err := r.write.Exec(`
		UPDATE issues SET title = ?, comicvine_id = ?, description = ?,
			cover_date = ?, store_date = ?, cover_url = ?,
			writers = ?, artists = ?, updated_at = ?
		WHERE id = ?`,
		issue.Title, issue.ComicVineID, issue.Description,
		issue.CoverDate, issue.StoreDate, issue.CoverURL,
		issue.Writers, issue.Artists,
		time.Now().UTC().Format(time.RFC3339), issue.ID,
	)
	return err
}

// FindByComicVineID finds an issue by its ComicVine ID.
func (r *IssueRepo) FindByComicVineID(cvID int64) (*model.Issue, error) {
	row := r.read.QueryRow(`
		SELECT i.id, i.series_id, i.issue_number, i.sort_number, COALESCE(i.title,''), i.comicvine_id,
			COALESCE(i.description,''), COALESCE(i.cover_date,''), COALESCE(i.store_date,''), COALESCE(i.cover_url,''), COALESCE(i.writers,''), COALESCE(i.artists,''),
			i.read_status, i.rating, i.last_read_page, i.metadata_locked, i.created_at, i.updated_at,
			0 as has_file, NULL as file_id, '' as series_title
		FROM issues i
		WHERE i.comicvine_id = ?`, cvID)
	return scanIssue(row)
}

// ListByDateRange returns issues with store_date in the given range.
// Optionally filters to only tracked series.
func (r *IssueRepo) ListByDateRange(start, end string, trackedOnly bool) ([]model.Issue, error) {
	whereClause := `WHERE i.store_date >= ? AND i.store_date <= ? AND i.store_date != ''`
	if trackedOnly {
		whereClause += ` AND s.tracked = 1`
	}

	query := fmt.Sprintf(`
		SELECT i.id, i.series_id, i.issue_number, i.sort_number, COALESCE(i.title,''), i.comicvine_id,
			COALESCE(i.description,''), COALESCE(i.cover_date,''), COALESCE(i.store_date,''), COALESCE(i.cover_url,''), COALESCE(i.writers,''), COALESCE(i.artists,''),
			i.read_status, i.rating, i.last_read_page, i.metadata_locked, i.created_at, i.updated_at,
			CASE WHEN cf.id IS NOT NULL THEN 1 ELSE 0 END as has_file,
			cf.id as file_id,
			s.title as series_title
		FROM issues i
		LEFT JOIN comic_files cf ON cf.issue_id = i.id
		JOIN series s ON i.series_id = s.id
		%s
		ORDER BY i.store_date ASC, s.title ASC, i.sort_number ASC`, whereClause)

	rows, err := r.read.Query(query, start, end)
	if err != nil {
		return nil, fmt.Errorf("listing issues by date range: %w", err)
	}
	defer rows.Close()

	var issues []model.Issue
	for rows.Next() {
		issue, err := scanIssueRow(rows)
		if err != nil {
			return nil, err
		}
		issues = append(issues, *issue)
	}
	return issues, nil
}

// UpdateLastReadPage saves the reader's progress for an issue.
func (r *IssueRepo) UpdateLastReadPage(id int64, page int) error {
	_, err := r.write.Exec(
		`UPDATE issues SET last_read_page = ?, updated_at = ? WHERE id = ?`,
		page, time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

func scanIssue(row *sql.Row) (*model.Issue, error) {
	i := &model.Issue{}
	var createdAt, updatedAt string
	err := row.Scan(
		&i.ID, &i.SeriesID, &i.IssueNumber, &i.SortNumber, &i.Title, &i.ComicVineID,
		&i.Description, &i.CoverDate, &i.StoreDate, &i.CoverURL, &i.Writers, &i.Artists,
		&i.ReadStatus, &i.Rating, &i.LastReadPage, &i.MetadataLocked, &createdAt, &updatedAt,
		&i.HasFile, &i.FileID, &i.SeriesTitle,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning issue: %w", err)
	}
	i.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	i.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return i, nil
}

func scanIssueRow(rows *sql.Rows) (*model.Issue, error) {
	i := &model.Issue{}
	var createdAt, updatedAt string
	err := rows.Scan(
		&i.ID, &i.SeriesID, &i.IssueNumber, &i.SortNumber, &i.Title, &i.ComicVineID,
		&i.Description, &i.CoverDate, &i.StoreDate, &i.CoverURL, &i.Writers, &i.Artists,
		&i.ReadStatus, &i.Rating, &i.LastReadPage, &i.MetadataLocked, &createdAt, &updatedAt,
		&i.HasFile, &i.FileID, &i.SeriesTitle,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning issue row: %w", err)
	}
	i.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	i.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return i, nil
}

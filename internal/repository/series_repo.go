package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jeremy/longbox/internal/model"
)

type SeriesRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewSeriesRepo(read, write *sql.DB) *SeriesRepo {
	return &SeriesRepo{read: read, write: write}
}

func (r *SeriesRepo) Create(s *model.Series) error {
	res, err := r.write.Exec(`
		INSERT INTO series (title, sort_title, year, publisher_id, description, status, total_issues, tracked)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		s.Title, s.SortTitle, s.Year, s.PublisherID, s.Description, s.Status, s.TotalIssues, s.Tracked,
	)
	if err != nil {
		return fmt.Errorf("inserting series: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting last insert id: %w", err)
	}
	s.ID = id
	return nil
}

func (r *SeriesRepo) GetByID(id int64) (*model.Series, error) {
	row := r.read.QueryRow(`
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			COALESCE((SELECT COUNT(*) FROM issues WHERE series_id = s.id), 0) as issue_count,
			COALESCE((SELECT COUNT(*) FROM comic_files cf JOIN issues i ON cf.issue_id = i.id WHERE i.series_id = s.id), 0) as file_count,
			COALESCE(p.name, '') as publisher_name
		FROM series s
		LEFT JOIN publishers p ON s.publisher_id = p.id
		WHERE s.id = ?`, id)
	return scanSeries(row)
}

// FindByTitleAndYear finds a series by title and optional year.
func (r *SeriesRepo) FindByTitleAndYear(title string, year *int) (*model.Series, error) {
	var row *sql.Row
	if year != nil {
		row = r.read.QueryRow(`
			SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id,
				s.description, s.status, s.total_issues, s.cover_file_id, s.tracked,
				s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
				0 as issue_count, 0 as file_count, '' as publisher_name
			FROM series s
			WHERE LOWER(s.title) = LOWER(?) AND s.year = ?`, title, *year)
	} else {
		row = r.read.QueryRow(`
			SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id,
				s.description, s.status, s.total_issues, s.cover_file_id, s.tracked,
				s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
				0 as issue_count, 0 as file_count, '' as publisher_name
			FROM series s
			WHERE LOWER(s.title) = LOWER(?) AND s.year IS NULL`, title)
	}
	return scanSeries(row)
}

func (r *SeriesRepo) List(page, perPage int, sortBy, order string, trackedOnly ...bool) ([]model.Series, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	}
	offset := (page - 1) * perPage

	// Validate sort column
	validSorts := map[string]string{
		"title":       "s.sort_title",
		"year":        "s.year",
		"issue_count": "issue_count",
		"updated_at":  "s.updated_at",
	}
	sortCol, ok := validSorts[sortBy]
	if !ok {
		sortCol = "s.sort_title"
	}
	if order != "desc" {
		order = "asc"
	}

	// Optional tracked filter
	whereClause := ""
	if len(trackedOnly) > 0 && trackedOnly[0] {
		whereClause = "WHERE s.tracked = 1"
	}

	var total int
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM series s %s`, whereClause)
	if err := r.read.QueryRow(countQuery).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting series: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			COALESCE((SELECT COUNT(*) FROM issues WHERE series_id = s.id), 0) as issue_count,
			COALESCE((SELECT COUNT(*) FROM comic_files cf JOIN issues i ON cf.issue_id = i.id WHERE i.series_id = s.id), 0) as file_count,
			COALESCE(p.name, '') as publisher_name
		FROM series s
		LEFT JOIN publishers p ON s.publisher_id = p.id
		%s
		ORDER BY %s %s
		LIMIT ? OFFSET ?`, whereClause, sortCol, order)

	rows, err := r.read.Query(query, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing series: %w", err)
	}
	defer rows.Close()

	var seriesList []model.Series
	for rows.Next() {
		s, err := scanSeriesRow(rows)
		if err != nil {
			return nil, 0, err
		}
		seriesList = append(seriesList, *s)
	}
	return seriesList, total, nil
}

// UpdateFromMetadata updates a series with ComicVine metadata.
func (r *SeriesRepo) UpdateFromMetadata(s *model.Series) error {
	_, err := r.write.Exec(`
		UPDATE series SET title = ?, sort_title = ?, year = ?, publisher_id = ?,
			comicvine_id = ?, description = ?, status = ?, total_issues = ?,
			last_cv_sync = ?, updated_at = ?
		WHERE id = ?`,
		s.Title, s.SortTitle, s.Year, s.PublisherID, s.ComicVineID,
		s.Description, s.Status, s.TotalIssues,
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339), s.ID,
	)
	return err
}

// FindByComicVineID finds a series by ComicVine ID.
func (r *SeriesRepo) FindByComicVineID(cvID int64) (*model.Series, error) {
	row := r.read.QueryRow(`
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			COALESCE((SELECT COUNT(*) FROM issues WHERE series_id = s.id), 0) as issue_count,
			COALESCE((SELECT COUNT(*) FROM comic_files cf JOIN issues i ON cf.issue_id = i.id WHERE i.series_id = s.id), 0) as file_count,
			COALESCE(p.name, '') as publisher_name
		FROM series s
		LEFT JOIN publishers p ON s.publisher_id = p.id
		WHERE s.comicvine_id = ?`, cvID)
	return scanSeries(row)
}

// SetTracked sets the tracked flag on a series.
func (r *SeriesRepo) SetTracked(id int64, tracked bool) error {
	_, err := r.write.Exec(`UPDATE series SET tracked = ?, updated_at = ? WHERE id = ?`,
		tracked, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// ListTracked returns all tracked series (no pagination — typically a small set).
func (r *SeriesRepo) ListTracked() ([]model.Series, error) {
	rows, err := r.read.Query(`
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			COALESCE((SELECT COUNT(*) FROM issues WHERE series_id = s.id), 0) as issue_count,
			COALESCE((SELECT COUNT(*) FROM comic_files cf JOIN issues i ON cf.issue_id = i.id WHERE i.series_id = s.id), 0) as file_count,
			COALESCE(p.name, '') as publisher_name
		FROM series s
		LEFT JOIN publishers p ON s.publisher_id = p.id
		WHERE s.tracked = 1
		ORDER BY s.sort_title ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing tracked series: %w", err)
	}
	defer rows.Close()

	var seriesList []model.Series
	for rows.Next() {
		s, err := scanSeriesRow(rows)
		if err != nil {
			return nil, err
		}
		seriesList = append(seriesList, *s)
	}
	return seriesList, nil
}

// ListWithComicVineID returns all series that have been matched to ComicVine.
func (r *SeriesRepo) ListWithComicVineID() ([]model.Series, error) {
	rows, err := r.read.Query(`
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			COALESCE((SELECT COUNT(*) FROM issues WHERE series_id = s.id), 0) as issue_count,
			COALESCE((SELECT COUNT(*) FROM comic_files cf JOIN issues i ON cf.issue_id = i.id WHERE i.series_id = s.id), 0) as file_count,
			COALESCE(p.name, '') as publisher_name
		FROM series s
		LEFT JOIN publishers p ON s.publisher_id = p.id
		WHERE s.comicvine_id IS NOT NULL
		ORDER BY s.sort_title ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing series with comicvine ID: %w", err)
	}
	defer rows.Close()

	var seriesList []model.Series
	for rows.Next() {
		s, err := scanSeriesRow(rows)
		if err != nil {
			return nil, err
		}
		seriesList = append(seriesList, *s)
	}
	return seriesList, nil
}

func (r *SeriesRepo) UpdateCoverFileID(seriesID, fileID int64) error {
	_, err := r.write.Exec(`UPDATE series SET cover_file_id = ?, updated_at = ? WHERE id = ?`,
		fileID, time.Now().UTC().Format(time.RFC3339), seriesID)
	return err
}

// SetParentSeries links a series as a child (annual) of a parent series.
func (r *SeriesRepo) SetParentSeries(id int64, parentID *int64) error {
	_, err := r.write.Exec(`UPDATE series SET parent_series_id = ?, updated_at = ? WHERE id = ?`,
		parentID, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// GetChildSeries returns all series that are children (annuals) of the given parent.
func (r *SeriesRepo) GetChildSeries(parentID int64) ([]model.Series, error) {
	rows, err := r.read.Query(`
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			COALESCE((SELECT COUNT(*) FROM issues WHERE series_id = s.id), 0) as issue_count,
			COALESCE((SELECT COUNT(*) FROM comic_files cf JOIN issues i ON cf.issue_id = i.id WHERE i.series_id = s.id), 0) as file_count,
			COALESCE(p.name, '') as publisher_name
		FROM series s
		LEFT JOIN publishers p ON s.publisher_id = p.id
		WHERE s.parent_series_id = ?
		ORDER BY s.sort_title ASC`, parentID)
	if err != nil {
		return nil, fmt.Errorf("listing child series: %w", err)
	}
	defer rows.Close()

	var seriesList []model.Series
	for rows.Next() {
		s, err := scanSeriesRow(rows)
		if err != nil {
			return nil, err
		}
		seriesList = append(seriesList, *s)
	}
	return seriesList, nil
}

func scanSeries(row *sql.Row) (*model.Series, error) {
	s := &model.Series{}
	var createdAt, updatedAt string
	err := row.Scan(
		&s.ID, &s.Title, &s.SortTitle, &s.Year, &s.PublisherID, &s.ComicVineID,
		&s.Description, &s.Status, &s.TotalIssues, &s.CoverFileID, &s.Tracked,
		&s.MetadataLocked, &s.LastCVSync, &s.ParentSeriesID, &createdAt, &updatedAt,
		&s.IssueCount, &s.FileCount, &s.PublisherName,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning series: %w", err)
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	s.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return s, nil
}

func scanSeriesRow(rows *sql.Rows) (*model.Series, error) {
	s := &model.Series{}
	var createdAt, updatedAt string
	err := rows.Scan(
		&s.ID, &s.Title, &s.SortTitle, &s.Year, &s.PublisherID, &s.ComicVineID,
		&s.Description, &s.Status, &s.TotalIssues, &s.CoverFileID, &s.Tracked,
		&s.MetadataLocked, &s.LastCVSync, &s.ParentSeriesID, &createdAt, &updatedAt,
		&s.IssueCount, &s.FileCount, &s.PublisherName,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning series row: %w", err)
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	s.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return s, nil
}

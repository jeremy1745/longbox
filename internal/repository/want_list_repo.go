package repository

import (
	"database/sql"
	"fmt"

	"github.com/jeremy/longbox/internal/model"
)

type WantListRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewWantListRepo(read, write *sql.DB) *WantListRepo {
	return &WantListRepo{read: read, write: write}
}

// Create adds an issue to the want list. Uses INSERT OR IGNORE since issue_id is UNIQUE.
func (r *WantListRepo) Create(issueID int64, priority int, notes string) (*model.WantListItem, error) {
	res, err := r.write.Exec(`
		INSERT OR IGNORE INTO want_list (issue_id, priority, notes)
		VALUES (?, ?, ?)`, issueID, priority, notes)
	if err != nil {
		return nil, fmt.Errorf("inserting want list item: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("getting last insert id: %w", err)
	}
	if id == 0 {
		// Already existed — fetch existing
		return r.GetByIssueID(issueID)
	}
	return r.GetByID(id)
}

// Delete removes a want list item by its ID.
func (r *WantListRepo) Delete(id int64) error {
	res, err := r.write.Exec(`DELETE FROM want_list WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("deleting want list item: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("want list item %d not found", id)
	}
	return nil
}

// DeleteByIssueID removes a want list item by issue ID.
func (r *WantListRepo) DeleteByIssueID(issueID int64) error {
	_, err := r.write.Exec(`DELETE FROM want_list WHERE issue_id = ?`, issueID)
	return err
}

// Update modifies the priority and notes of a want list item.
func (r *WantListRepo) Update(id int64, priority int, notes string) error {
	res, err := r.write.Exec(`UPDATE want_list SET priority = ?, notes = ? WHERE id = ?`,
		priority, notes, id)
	if err != nil {
		return fmt.Errorf("updating want list item: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("want list item %d not found", id)
	}
	return nil
}

// GetByID returns a want list item with joined issue/series info.
func (r *WantListRepo) GetByID(id int64) (*model.WantListItem, error) {
	row := r.read.QueryRow(`
		SELECT w.id, w.issue_id, w.priority, COALESCE(w.notes,''), w.added_at,
			i.issue_number, i.series_id, COALESCE(s.title,'') as series_title,
			COALESCE(i.cover_url,'') as cover_url,
			COALESCE(i.store_date,'') as store_date,
			COALESCE(i.cover_date,'') as cover_date
		FROM want_list w
		JOIN issues i ON w.issue_id = i.id
		JOIN series s ON i.series_id = s.id
		WHERE w.id = ?`, id)
	return scanWantListItem(row)
}

// GetByIssueID returns a want list item by issue ID.
func (r *WantListRepo) GetByIssueID(issueID int64) (*model.WantListItem, error) {
	row := r.read.QueryRow(`
		SELECT w.id, w.issue_id, w.priority, COALESCE(w.notes,''), w.added_at,
			i.issue_number, i.series_id, COALESCE(s.title,'') as series_title,
			COALESCE(i.cover_url,'') as cover_url,
			COALESCE(i.store_date,'') as store_date,
			COALESCE(i.cover_date,'') as cover_date
		FROM want_list w
		JOIN issues i ON w.issue_id = i.id
		JOIN series s ON i.series_id = s.id
		WHERE w.issue_id = ?`, issueID)
	return scanWantListItem(row)
}

// List returns paginated want list items with joined info, sorted by priority or date.
func (r *WantListRepo) List(page, perPage int, sortBy, order string) ([]model.WantListItem, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	}
	offset := (page - 1) * perPage

	validSorts := map[string]string{
		"priority":   "w.priority",
		"series":     "s.title",
		"date":       "w.added_at",
		"store_date": "i.store_date",
		"issue":      "i.sort_number",
	}
	sortCol, ok := validSorts[sortBy]
	if !ok {
		sortCol = "w.priority DESC, s.title"
	}
	if order != "asc" {
		order = "desc"
	}

	var total int
	if err := r.read.QueryRow(`SELECT COUNT(*) FROM want_list`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting want list: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT w.id, w.issue_id, w.priority, COALESCE(w.notes,''), w.added_at,
			i.issue_number, i.series_id, COALESCE(s.title,'') as series_title,
			COALESCE(i.cover_url,'') as cover_url,
			COALESCE(i.store_date,'') as store_date,
			COALESCE(i.cover_date,'') as cover_date
		FROM want_list w
		JOIN issues i ON w.issue_id = i.id
		JOIN series s ON i.series_id = s.id
		ORDER BY %s %s
		LIMIT ? OFFSET ?`, sortCol, order)

	rows, err := r.read.Query(query, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing want list: %w", err)
	}
	defer rows.Close()

	var items []model.WantListItem
	for rows.Next() {
		item, err := scanWantListItemRow(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}
	return items, total, nil
}

// AddMissingForSeries bulk-adds missing issues for a series to the want list.
// A "missing" issue is one that has no comic_file linked to it.
func (r *WantListRepo) AddMissingForSeries(seriesID int64) (int, error) {
	res, err := r.write.Exec(`
		INSERT OR IGNORE INTO want_list (issue_id, priority, notes)
		SELECT i.id, 0, ''
		FROM issues i
		LEFT JOIN comic_files cf ON cf.issue_id = i.id
		WHERE i.series_id = ? AND cf.id IS NULL
		AND i.id NOT IN (SELECT issue_id FROM want_list)`, seriesID)
	if err != nil {
		return 0, fmt.Errorf("adding missing issues to want list: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// RemoveForSeries removes all want list entries for issues in a given series.
func (r *WantListRepo) RemoveForSeries(seriesID int64) (int, error) {
	res, err := r.write.Exec(`
		DELETE FROM want_list
		WHERE issue_id IN (SELECT id FROM issues WHERE series_id = ?)`, seriesID)
	if err != nil {
		return 0, fmt.Errorf("removing want list items for series: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ListBySeriesID returns want list items for a specific series.
func (r *WantListRepo) ListBySeriesID(seriesID int64) ([]model.WantListItem, error) {
	rows, err := r.read.Query(`
		SELECT w.id, w.issue_id, w.priority, COALESCE(w.notes,''), w.added_at,
			i.issue_number, i.series_id, COALESCE(s.title,'') as series_title,
			COALESCE(i.cover_url,'') as cover_url,
			COALESCE(i.store_date,'') as store_date,
			COALESCE(i.cover_date,'') as cover_date
		FROM want_list w
		JOIN issues i ON w.issue_id = i.id
		JOIN series s ON i.series_id = s.id
		WHERE i.series_id = ?
		ORDER BY i.sort_number ASC`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("listing want list for series: %w", err)
	}
	defer rows.Close()

	var items []model.WantListItem
	for rows.Next() {
		item, err := scanWantListItemRow(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, *item)
	}
	return items, nil
}

func scanWantListItem(row *sql.Row) (*model.WantListItem, error) {
	item := &model.WantListItem{}
	err := row.Scan(
		&item.ID, &item.IssueID, &item.Priority, &item.Notes, &item.AddedAt,
		&item.IssueNumber, &item.SeriesID, &item.SeriesTitle,
		&item.CoverURL, &item.StoreDate, &item.CoverDate,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning want list item: %w", err)
	}
	return item, nil
}

func scanWantListItemRow(rows *sql.Rows) (*model.WantListItem, error) {
	item := &model.WantListItem{}
	err := rows.Scan(
		&item.ID, &item.IssueID, &item.Priority, &item.Notes, &item.AddedAt,
		&item.IssueNumber, &item.SeriesID, &item.SeriesTitle,
		&item.CoverURL, &item.StoreDate, &item.CoverDate,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning want list item row: %w", err)
	}
	return item, nil
}

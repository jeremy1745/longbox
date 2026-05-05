package repository

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
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
		SELECT i.id, i.series_id, i.issue_number, i.sort_number, COALESCE(i.title,''), i.comicvine_id, i.metron_id,
			COALESCE(i.description,''), COALESCE(i.cover_date,''), COALESCE(i.store_date,''), COALESCE(i.cover_url,''), COALESCE(i.writers,''), COALESCE(i.artists,''),
			i.read_status, i.skip_status, i.rating, i.last_read_page, i.metadata_locked, i.created_at, i.updated_at,
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
		SELECT i.id, i.series_id, i.issue_number, i.sort_number, COALESCE(i.title,''), i.comicvine_id, i.metron_id,
			COALESCE(i.description,''), COALESCE(i.cover_date,''), COALESCE(i.store_date,''), COALESCE(i.cover_url,''), COALESCE(i.writers,''), COALESCE(i.artists,''),
			i.read_status, i.skip_status, i.rating, i.last_read_page, i.metadata_locked, i.created_at, i.updated_at,
			0 as has_file, NULL as file_id, '' as series_title
		FROM issues i
		WHERE i.series_id = ? AND i.issue_number = ?`, seriesID, number)
	return scanIssue(row)
}

// ListAllGroupedBySeries returns every issue keyed by series_id in one
// query. Bulk maintenance jobs (sidecar writer, folder-image refresh)
// use this to avoid N round-trips of ListBySeries — one scan + in-memory
// index beats thousands of small JOIN queries when iterating the full
// catalog.
func (r *IssueRepo) ListAllGroupedBySeries() (map[int64][]model.Issue, error) {
	rows, err := r.read.Query(`
		SELECT i.id, i.series_id, i.issue_number, i.sort_number, COALESCE(i.title,''), i.comicvine_id, i.metron_id,
			COALESCE(i.description,''), COALESCE(i.cover_date,''), COALESCE(i.store_date,''), COALESCE(i.cover_url,''), COALESCE(i.writers,''), COALESCE(i.artists,''),
			i.read_status, i.skip_status, i.rating, i.last_read_page, i.metadata_locked, i.created_at, i.updated_at,
			CASE WHEN cf.id IS NOT NULL THEN 1 ELSE 0 END as has_file,
			cf.id as file_id,
			s.title as series_title
		FROM issues i
		LEFT JOIN comic_files cf ON cf.issue_id = i.id
		LEFT JOIN series s ON i.series_id = s.id
		ORDER BY i.series_id, i.sort_number ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing all issues grouped: %w", err)
	}
	defer rows.Close()

	out := make(map[int64][]model.Issue)
	for rows.Next() {
		issue, err := scanIssueRow(rows)
		if err != nil {
			return nil, err
		}
		out[issue.SeriesID] = append(out[issue.SeriesID], *issue)
	}
	return out, rows.Err()
}

// ListBySeries returns all issues for a given series, sorted by sort_number.
func (r *IssueRepo) ListBySeries(seriesID int64) ([]model.Issue, error) {
	rows, err := r.read.Query(`
		SELECT i.id, i.series_id, i.issue_number, i.sort_number, COALESCE(i.title,''), i.comicvine_id, i.metron_id,
			COALESCE(i.description,''), COALESCE(i.cover_date,''), COALESCE(i.store_date,''), COALESCE(i.cover_url,''), COALESCE(i.writers,''), COALESCE(i.artists,''),
			i.read_status, i.skip_status, i.rating, i.last_read_page, i.metadata_locked, i.created_at, i.updated_at,
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

func (r *IssueRepo) UpdateTitleAndNumber(id int64, title, issueNumber string, sortNumber float64) error {
	_, err := r.write.Exec(`UPDATE issues SET title = ?, issue_number = ?, sort_number = ?, metadata_locked = 1, updated_at = ? WHERE id = ?`,
		title, issueNumber, sortNumber, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (r *IssueRepo) Delete(id int64) error {
	_, err := r.write.Exec(`DELETE FROM issues WHERE id = ?`, id)
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

// UpdateFromMetronMetadata updates an issue with Metron-sourced metadata.
// Writes metron_id and (when Metron carries one) cross-references comicvine_id.
func (r *IssueRepo) UpdateFromMetronMetadata(issue *model.Issue) error {
	_, err := r.write.Exec(`
		UPDATE issues SET title = ?, metron_id = ?,
			comicvine_id = COALESCE(?, comicvine_id),
			description = ?,
			cover_date = ?, store_date = ?, cover_url = ?,
			writers = ?, artists = ?, updated_at = ?
		WHERE id = ?`,
		issue.Title, issue.MetronID, issue.ComicVineID,
		issue.Description,
		issue.CoverDate, issue.StoreDate, issue.CoverURL,
		issue.Writers, issue.Artists,
		time.Now().UTC().Format(time.RFC3339), issue.ID,
	)
	return err
}

// SetMetronID writes only the metron_id column.
func (r *IssueRepo) SetMetronID(id int64, metronID *int64) error {
	_, err := r.write.Exec(
		`UPDATE issues SET metron_id = ?, updated_at = ? WHERE id = ?`,
		metronID, time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// FindByMetronID finds an issue by its Metron ID.
func (r *IssueRepo) FindByMetronID(metronID int64) (*model.Issue, error) {
	row := r.read.QueryRow(`
		SELECT i.id, i.series_id, i.issue_number, i.sort_number, COALESCE(i.title,''), i.comicvine_id, i.metron_id,
			COALESCE(i.description,''), COALESCE(i.cover_date,''), COALESCE(i.store_date,''), COALESCE(i.cover_url,''), COALESCE(i.writers,''), COALESCE(i.artists,''),
			i.read_status, i.skip_status, i.rating, i.last_read_page, i.metadata_locked, i.created_at, i.updated_at,
			0 as has_file, NULL as file_id, '' as series_title
		FROM issues i
		WHERE i.metron_id = ?`, metronID)
	return scanIssue(row)
}

// FindByComicVineID finds an issue by its ComicVine ID.
func (r *IssueRepo) FindByComicVineID(cvID int64) (*model.Issue, error) {
	row := r.read.QueryRow(`
		SELECT i.id, i.series_id, i.issue_number, i.sort_number, COALESCE(i.title,''), i.comicvine_id, i.metron_id,
			COALESCE(i.description,''), COALESCE(i.cover_date,''), COALESCE(i.store_date,''), COALESCE(i.cover_url,''), COALESCE(i.writers,''), COALESCE(i.artists,''),
			i.read_status, i.skip_status, i.rating, i.last_read_page, i.metadata_locked, i.created_at, i.updated_at,
			0 as has_file, NULL as file_id, '' as series_title
		FROM issues i
		WHERE i.comicvine_id = ?`, cvID)
	return scanIssue(row)
}

// ListOwnedByDateRange returns issues whose store_date falls in [start,end]
// AND that have a linked comic_files row. Useful for "new releases" surfaces
// where you don't want backfilled old issues to count.
func (r *IssueRepo) ListOwnedByDateRange(start, end string) ([]model.Issue, error) {
	rows, err := r.read.Query(`
		SELECT i.id, i.series_id, i.issue_number, i.sort_number, COALESCE(i.title,''), i.comicvine_id, i.metron_id,
			COALESCE(i.description,''), COALESCE(i.cover_date,''), COALESCE(i.store_date,''), COALESCE(i.cover_url,''), COALESCE(i.writers,''), COALESCE(i.artists,''),
			i.read_status, i.skip_status, i.rating, i.last_read_page, i.metadata_locked, i.created_at, i.updated_at,
			1 as has_file,
			cf.id as file_id,
			s.title as series_title
		FROM issues i
		JOIN comic_files cf ON cf.issue_id = i.id
		JOIN series s ON s.id = i.series_id
		WHERE i.store_date >= ? AND i.store_date <= ? AND i.store_date != ''
		ORDER BY i.store_date DESC, s.title ASC, i.sort_number ASC`,
		start, end,
	)
	if err != nil {
		return nil, fmt.Errorf("listing owned issues by date range: %w", err)
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

// ListByDateRange returns issues with store_date in the given range.
// Optionally filters to only tracked series.
func (r *IssueRepo) ListByDateRange(start, end string, trackedOnly bool) ([]model.Issue, error) {
	whereClause := `WHERE i.store_date >= ? AND i.store_date <= ? AND i.store_date != ''`
	if trackedOnly {
		whereClause += ` AND s.tracked = 1`
	}

	query := fmt.Sprintf(`
		SELECT i.id, i.series_id, i.issue_number, i.sort_number, COALESCE(i.title,''), i.comicvine_id, i.metron_id,
			COALESCE(i.description,''), COALESCE(i.cover_date,''), COALESCE(i.store_date,''), COALESCE(i.cover_url,''), COALESCE(i.writers,''), COALESCE(i.artists,''),
			i.read_status, i.skip_status, i.rating, i.last_read_page, i.metadata_locked, i.created_at, i.updated_at,
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

// DuplicateIssueGroup names a (series_id, issue_number) pair that has more
// than one row in the issues table.
type DuplicateIssueGroup struct {
	SeriesID    int64
	IssueNumber string
	IssueIDs    []int64
}

// FindDuplicateIssueGroups returns every (series_id, issue_number) pair that
// has more than one issue row. Used by the dedupe maintenance flow.
//
// `issue_number` is normalized for grouping: leading zeros stripped via
// LTRIM so "6" and "006" group together. Without this normalization the
// reorg's canonical-path collision detector reports "two distinct DB rows
// want the same path" for what's actually one logical issue with two
// different padded representations across imports.
func (r *IssueRepo) FindDuplicateIssueGroups() ([]DuplicateIssueGroup, error) {
	rows, err := r.read.Query(`
		SELECT series_id,
		       CASE WHEN LTRIM(issue_number,'0') = '' THEN '0' ELSE LTRIM(issue_number,'0') END AS norm_num,
		       GROUP_CONCAT(id),
		       MIN(issue_number)
		FROM issues
		GROUP BY series_id, norm_num
		HAVING COUNT(*) > 1
	`)
	if err != nil {
		return nil, fmt.Errorf("listing duplicate issue groups: %w", err)
	}
	defer rows.Close()

	var groups []DuplicateIssueGroup
	for rows.Next() {
		var g DuplicateIssueGroup
		var ids, normNum string
		if err := rows.Scan(&g.SeriesID, &normNum, &ids, &g.IssueNumber); err != nil {
			return nil, fmt.Errorf("scanning duplicate issue group: %w", err)
		}
		_ = normNum
		for _, idStr := range strings.Split(ids, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
			if err != nil {
				continue
			}
			g.IssueIDs = append(g.IssueIDs, id)
		}
		if len(g.IssueIDs) > 1 {
			groups = append(groups, g)
		}
	}
	return groups, nil
}

// RelinkIssueRefs moves all dependent rows pointing at oldIDs onto newID.
// Touches comic_files, download_history, backlog_items, and copies want_list /
// story_arc_issues entries onto newID via INSERT OR IGNORE so the canonical
// inherits any extant want or arc membership before the duplicates get
// CASCADE-deleted.
func (r *IssueRepo) RelinkIssueRefs(newID int64, oldIDs []int64) (filesRelinked int64, wantsCopied int64, arcLinksCopied int64, err error) {
	if len(oldIDs) == 0 {
		return 0, 0, 0, nil
	}
	placeholders := strings.Repeat("?,", len(oldIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := func(extras ...any) []any {
		out := make([]any, 0, len(extras)+len(oldIDs))
		out = append(out, extras...)
		for _, id := range oldIDs {
			out = append(out, id)
		}
		return out
	}

	res, err := r.write.Exec(fmt.Sprintf(
		`UPDATE comic_files SET issue_id = ?, updated_at = ? WHERE issue_id IN (%s)`, placeholders),
		args(newID, time.Now().UTC().Format(time.RFC3339))...)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("relinking comic_files: %w", err)
	}
	filesRelinked, _ = res.RowsAffected()

	if _, err := r.write.Exec(fmt.Sprintf(
		`UPDATE download_history SET issue_id = ? WHERE issue_id IN (%s)`, placeholders),
		args(newID)...); err != nil {
		return 0, 0, 0, fmt.Errorf("relinking download_history: %w", err)
	}

	if _, err := r.write.Exec(fmt.Sprintf(
		`UPDATE backlog_items SET issue_id = ? WHERE issue_id IN (%s)`, placeholders),
		args(newID)...); err != nil {
		return 0, 0, 0, fmt.Errorf("relinking backlog_items: %w", err)
	}

	wRes, err := r.write.Exec(fmt.Sprintf(
		`INSERT OR IGNORE INTO want_list (issue_id, priority, notes, added_at)
		 SELECT ?, priority, notes, added_at FROM want_list WHERE issue_id IN (%s)`, placeholders),
		args(newID)...)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("copying want_list to canonical: %w", err)
	}
	wantsCopied, _ = wRes.RowsAffected()

	aRes, err := r.write.Exec(fmt.Sprintf(
		`INSERT OR IGNORE INTO story_arc_issues (story_arc_id, issue_id, sequence_number)
		 SELECT story_arc_id, ?, sequence_number FROM story_arc_issues WHERE issue_id IN (%s)`, placeholders),
		args(newID)...)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("copying story_arc_issues to canonical: %w", err)
	}
	arcLinksCopied, _ = aRes.RowsAffected()

	return filesRelinked, wantsCopied, arcLinksCopied, nil
}

// DeleteByIDs removes a batch of issue rows. CASCADE rules clean up any
// remaining want_list / story_arc_issues entries that point at the deleted ids.
func (r *IssueRepo) DeleteByIDs(ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	res, err := r.write.Exec(fmt.Sprintf(`DELETE FROM issues WHERE id IN (%s)`, placeholders), args...)
	if err != nil {
		return 0, fmt.Errorf("deleting issues: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// PickCanonicalIssueID returns the preferred canonical row from a duplicate
// group. Preference order: has comicvine_id, then metadata_locked, then
// lowest id. The remaining ids are the duplicates to merge into canonical.
func (r *IssueRepo) PickCanonicalIssueID(ids []int64) (int64, []int64, error) {
	if len(ids) == 0 {
		return 0, nil, fmt.Errorf("empty id list")
	}
	if len(ids) == 1 {
		return ids[0], nil, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := r.read.Query(fmt.Sprintf(`
		SELECT id FROM issues
		WHERE id IN (%s)
		ORDER BY (CASE WHEN comicvine_id IS NOT NULL THEN 0 ELSE 1 END),
				 metadata_locked DESC,
				 id ASC
	`, placeholders), args...)
	if err != nil {
		return 0, nil, fmt.Errorf("ranking issue ids: %w", err)
	}
	defer rows.Close()

	var ranked []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return 0, nil, err
		}
		ranked = append(ranked, id)
	}
	if len(ranked) == 0 {
		return 0, nil, fmt.Errorf("no rows returned for ids")
	}
	return ranked[0], ranked[1:], nil
}

// UpdateSeriesID reassigns an issue to a different series.
func (r *IssueRepo) UpdateSeriesID(id, newSeriesID int64) error {
	_, err := r.write.Exec(
		`UPDATE issues SET series_id = ?, updated_at = ? WHERE id = ?`,
		newSeriesID, time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// BackfillReadStatusFromProgress promotes every "reading" issue whose linked
// file has a known page_count and whose last_read_page is at or past the end
// to "read". Returns the number of rows updated. One-shot admin operation
// for healing pre-existing rows after the auto-promotion logic landed.
func (r *IssueRepo) BackfillReadStatusFromProgress() (int64, error) {
	res, err := r.write.Exec(`
		UPDATE issues
		SET read_status = 'read', updated_at = (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		WHERE read_status = 'reading'
		  AND last_read_page IS NOT NULL
		  AND id IN (
		    SELECT i.id FROM issues i
		    JOIN comic_files cf ON cf.issue_id = i.id
		    WHERE i.read_status = 'reading'
		      AND cf.page_count > 0
		      AND i.last_read_page >= cf.page_count - 1
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("backfilling read status: %w", err)
	}
	return res.RowsAffected()
}

// UpdateLastReadPage saves the reader's progress for an issue.
func (r *IssueRepo) UpdateLastReadPage(id int64, page int) error {
	_, err := r.write.Exec(
		`UPDATE issues SET last_read_page = ?, updated_at = ? WHERE id = ?`,
		page, time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// SetSkipStatus sets the skip status of an issue.
func (r *IssueRepo) SetSkipStatus(id int64, status *string) error {
	_, err := r.write.Exec(`UPDATE issues SET skip_status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// BulkSetSkipStatus updates skip_status for all issues in a series matching a filter.
func (r *IssueRepo) BulkSetSkipStatus(seriesID int64, fromStatus, toStatus *string) (int64, error) {
	var res sql.Result
	var err error
	now := time.Now().UTC().Format(time.RFC3339)

	if fromStatus == nil {
		res, err = r.write.Exec(`UPDATE issues SET skip_status = ?, updated_at = ? WHERE series_id = ? AND skip_status IS NULL`,
			toStatus, now, seriesID)
	} else {
		res, err = r.write.Exec(`UPDATE issues SET skip_status = ?, updated_at = ? WHERE series_id = ? AND skip_status = ?`,
			toStatus, now, seriesID, *fromStatus)
	}
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func scanIssue(row *sql.Row) (*model.Issue, error) {
	i := &model.Issue{}
	var createdAt, updatedAt string
	err := row.Scan(
		&i.ID, &i.SeriesID, &i.IssueNumber, &i.SortNumber, &i.Title, &i.ComicVineID, &i.MetronID,
		&i.Description, &i.CoverDate, &i.StoreDate, &i.CoverURL, &i.Writers, &i.Artists,
		&i.ReadStatus, &i.SkipStatus, &i.Rating, &i.LastReadPage, &i.MetadataLocked, &createdAt, &updatedAt,
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
		&i.ID, &i.SeriesID, &i.IssueNumber, &i.SortNumber, &i.Title, &i.ComicVineID, &i.MetronID,
		&i.Description, &i.CoverDate, &i.StoreDate, &i.CoverURL, &i.Writers, &i.Artists,
		&i.ReadStatus, &i.SkipStatus, &i.Rating, &i.LastReadPage, &i.MetadataLocked, &createdAt, &updatedAt,
		&i.HasFile, &i.FileID, &i.SeriesTitle,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning issue row: %w", err)
	}
	i.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	i.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return i, nil
}

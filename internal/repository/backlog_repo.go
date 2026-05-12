package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jeremy/longbox/internal/model"
)

const backlogItemSelect = `
	SELECT bi.id, bi.backlog_run_id, bi.series_id, bi.issue_id,
		COALESCE(bi.variant_name,''), bi.priority, bi.status,
		bi.retry_count, COALESCE(bi.retry_at,''), COALESCE(bi.last_error,''),
		COALESCE(bi.sab_nzo_id,''), COALESCE(bi.nzb_guid,''),
		bi.download_history_id, bi.created_at, bi.updated_at,
		COALESCE(i.issue_number,''), COALESCE(se.title,'')
	FROM backlog_items bi
	LEFT JOIN issues i ON i.id = bi.issue_id
	LEFT JOIN series se ON se.id = bi.series_id
`

type BacklogRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewBacklogRepo(read, write *sql.DB) *BacklogRepo {
	return &BacklogRepo{read: read, write: write}
}

func (r *BacklogRepo) CreateRun(seriesID int64, includeVariants bool) (*model.BacklogRun, error) {
	res, err := r.write.Exec(`
		INSERT INTO backlog_runs (series_id, include_variants)
		VALUES (?, ?)
	`, seriesID, boolToInt(includeVariants))
	if err != nil {
		return nil, fmt.Errorf("creating backlog run: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("reading backlog run id: %w", err)
	}
	return r.GetRunByID(id)
}

func (r *BacklogRepo) GetRunByID(id int64) (*model.BacklogRun, error) {
	row := r.read.QueryRow(`
		SELECT br.id, br.series_id, COALESCE(s.title,''), br.status, br.include_variants,
			br.total_issues, br.queued_issues, br.completed_issues, br.failed_issues, br.paused,
			br.created_at, br.updated_at
		FROM backlog_runs br
		JOIN series s ON s.id = br.series_id
		WHERE br.id = ?
	`, id)
	return scanBacklogRun(row)
}

func (r *BacklogRepo) ListRuns(page, perPage int) ([]model.BacklogRun, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	}
	offset := (page - 1) * perPage

	var total int
	if err := r.read.QueryRow(`SELECT COUNT(*) FROM backlog_runs`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting backlog runs: %w", err)
	}

	rows, err := r.read.Query(`
		SELECT br.id, br.series_id, COALESCE(s.title,''), br.status, br.include_variants,
			br.total_issues, br.queued_issues, br.completed_issues, br.failed_issues, br.paused,
			br.created_at, br.updated_at
		FROM backlog_runs br
		JOIN series s ON s.id = br.series_id
		ORDER BY br.created_at DESC
		LIMIT ? OFFSET ?
	`, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing backlog runs: %w", err)
	}
	defer rows.Close()

	var runs []model.BacklogRun
	for rows.Next() {
		run, err := scanBacklogRunRow(rows)
		if err != nil {
			return nil, 0, err
		}
		runs = append(runs, *run)
	}

	return runs, total, nil
}

func (r *BacklogRepo) InsertItems(items []model.BacklogItem) error {
	if len(items) == 0 {
		return nil
	}
	tx, err := r.write.Begin()
	if err != nil {
		return fmt.Errorf("starting backlog insert tx: %w", err)
	}
	stmt, err := tx.Prepare(`
		INSERT INTO backlog_items (
			backlog_run_id, series_id, issue_id, variant_name,
			priority, status
		) VALUES (?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("preparing backlog insert: %w", err)
	}
	defer stmt.Close()

	for _, item := range items {
		status := item.Status
		if status == "" {
			status = "pending"
		}
		if _, err := stmt.Exec(
			item.BacklogRunID,
			item.SeriesID,
			item.IssueID,
			item.VariantName,
			item.Priority,
			status,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("inserting backlog item: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing backlog items: %w", err)
	}
	return nil
}

func (r *BacklogRepo) UpdateRunCounts(id int64, total, queued, completed, failed int, status string) error {
	if status == "" {
		status = "ready"
	}
	_, err := r.write.Exec(`
		UPDATE backlog_runs
		SET total_issues = ?,
			queued_issues = ?,
			completed_issues = ?,
			failed_issues = ?,
			status = ?,
			updated_at = (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		WHERE id = ?
	`, total, queued, completed, failed, status, id)
	if err != nil {
		return fmt.Errorf("updating backlog run counts: %w", err)
	}
	return nil
}

func (r *BacklogRepo) CountActiveDownloads() (int, error) {
	var count int
	if err := r.read.QueryRow(`
		SELECT COUNT(*)
		FROM backlog_items bi
		JOIN backlog_runs br ON br.id = bi.backlog_run_id
		WHERE br.paused = 0 AND bi.status IN ('queued','downloading')
	`).Scan(&count); err != nil {
		return 0, fmt.Errorf("counting active backlog downloads: %w", err)
	}
	return count, nil
}

func (r *BacklogRepo) SetRunPaused(id int64, paused bool) error {
	if paused {
		_, err := r.write.Exec(`
			UPDATE backlog_runs
			SET paused = 1, status = 'paused', updated_at = (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
			WHERE id = ?
		`, id)
		return err
	}
	_, err := r.write.Exec(`
		UPDATE backlog_runs
		SET paused = 0, updated_at = (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		WHERE id = ?
	`, id)
	if err != nil {
		return err
	}
	return r.RefreshRunCounts(id)
}

func (r *BacklogRepo) ListItems(runID int64, status string, page, perPage int) ([]model.BacklogItem, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	}
	offset := (page - 1) * perPage

	params := []interface{}{runID}
	countQuery := `SELECT COUNT(*) FROM backlog_items WHERE backlog_run_id = ?`
	if status != "" {
		countQuery += " AND status = ?"
		params = append(params, status)
	}
	var total int
	if err := r.read.QueryRow(countQuery, params...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting backlog items: %w", err)
	}

	query := backlogItemSelect + ` WHERE bi.backlog_run_id = ?`
	args := []interface{}{runID}
	if status != "" {
		query += " AND bi.status = ?"
		args = append(args, status)
	}
	query += " ORDER BY bi.created_at ASC LIMIT ? OFFSET ?"
	args = append(args, perPage, offset)

	rows, err := r.read.Query(query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing backlog items: %w", err)
	}
	defer rows.Close()

	var items []model.BacklogItem
	for rows.Next() {
		item, err := scanBacklogItemRow(rows)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, *item)
	}

	return items, total, nil
}

func (r *BacklogRepo) RetryItem(id int64) (*model.BacklogItem, error) {
	row := r.read.QueryRow(backlogItemSelect+" WHERE bi.id = ?", id)
	item, err := scanBacklogItem(row)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, sql.ErrNoRows
	}
	_, err = r.write.Exec(`
		UPDATE backlog_items
		SET status = 'pending', retry_at = NULL, last_error = '',
			download_history_id = NULL, sab_nzo_id = '', nzb_guid = '',
			updated_at = (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		WHERE id = ?
	`, id)
	if err != nil {
		return nil, fmt.Errorf("retry backlog item: %w", err)
	}
	return item, nil
}

func (r *BacklogRepo) FindNextCandidate(maxRetries int, now time.Time) (*model.BacklogItem, error) {
	query := backlogItemSelect + `
		JOIN backlog_runs br ON br.id = bi.backlog_run_id
		WHERE br.paused = 0
		  AND (bi.status = 'pending'
		       OR (bi.status = 'failed' AND (bi.retry_at = '' OR bi.retry_at <= ?) AND (? <= 0 OR bi.retry_count < ?)))
		ORDER BY CASE bi.status WHEN 'pending' THEN 0 ELSE 1 END, bi.created_at ASC
		LIMIT 1
	`
	retryLimit := maxRetries
	if retryLimit < 0 {
		retryLimit = 0
	}
	row := r.read.QueryRow(query, now.UTC().Format(time.RFC3339), maxRetries, maxRetries)
	return scanBacklogItem(row)
}

func (r *BacklogRepo) UpdateItemStatus(id int64, status, lastError string, retryAt *time.Time) error {
	var retryVal interface{}
	if retryAt != nil {
		retryVal = retryAt.UTC().Format(time.RFC3339)
	}
	_, err := r.write.Exec(`
		UPDATE backlog_items
		SET status = ?, last_error = ?, retry_at = ?, updated_at = (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		WHERE id = ?
	`, status, lastError, retryVal, id)
	if err != nil {
		return fmt.Errorf("updating backlog item status: %w", err)
	}
	return nil
}

func (r *BacklogRepo) AttachDownload(id int64, downloadHistoryID int64, sabID, nzbGuid string) error {
	_, err := r.write.Exec(`
		UPDATE backlog_items
		SET download_history_id = ?, sab_nzo_id = ?, nzb_guid = ?, status = 'queued',
			updated_at = (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		WHERE id = ?
	`, downloadHistoryID, sabID, nzbGuid, id)
	if err != nil {
		return fmt.Errorf("attaching download to backlog item: %w", err)
	}
	return nil
}

func (r *BacklogRepo) MarkFailure(id int64, lastError string, retryAt *time.Time, exhausted bool) error {
	var retryVal interface{}
	if retryAt != nil {
		retryVal = retryAt.UTC().Format(time.RFC3339)
	}
	status := "failed"
	if exhausted {
		status = "error"
	}
	_, err := r.write.Exec(`
		UPDATE backlog_items
		SET status = ?, last_error = ?, retry_at = ?, retry_count = retry_count + 1,
			download_history_id = NULL, sab_nzo_id = '', updated_at = (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		WHERE id = ?
	`, status, lastError, retryVal, id)
	if err != nil {
		return fmt.Errorf("marking backlog failure: %w", err)
	}
	return nil
}

func (r *BacklogRepo) MarkCompleted(id int64) error {
	_, err := r.write.Exec(`
		UPDATE backlog_items
		SET status = 'completed', last_error = '', retry_at = NULL,
			sab_nzo_id = '', updated_at = (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
		WHERE id = ?
	`, id)
	if err != nil {
		return fmt.Errorf("marking backlog item complete: %w", err)
	}
	return nil
}

func (r *BacklogRepo) FindByDownloadHistory(downloadHistoryID int64) (*model.BacklogItem, error) {
	row := r.read.QueryRow(backlogItemSelect+` WHERE bi.download_history_id = ? LIMIT 1`, downloadHistoryID)
	return scanBacklogItem(row)
}

func (r *BacklogRepo) RefreshRunCounts(runID int64) error {
	row := r.read.QueryRow(`
		SELECT
			COUNT(*) AS total,
			COALESCE(SUM(CASE WHEN status IN ('pending','searching','queued','downloading') THEN 1 ELSE 0 END),0) AS queued,
			COALESCE(SUM(CASE WHEN status = 'completed' THEN 1 ELSE 0 END),0) AS completed,
			COALESCE(SUM(CASE WHEN status IN ('failed','error') THEN 1 ELSE 0 END),0) AS failed
		FROM backlog_items
		WHERE backlog_run_id = ?
	`, runID)
	var total, queued, completed, failed int
	if err := row.Scan(&total, &queued, &completed, &failed); err != nil {
		return fmt.Errorf("refreshing backlog run counts: %w", err)
	}
	status := "completed"
	if queued > 0 {
		status = "ready"
	} else if failed > 0 {
		status = "attention"
	}
	return r.UpdateRunCounts(runID, total, queued, completed, failed, status)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func scanBacklogRun(row *sql.Row) (*model.BacklogRun, error) {
	run := &model.BacklogRun{}
	var includeVariants int
	var paused int
	if err := row.Scan(
		&run.ID,
		&run.SeriesID,
		&run.SeriesTitle,
		&run.Status,
		&includeVariants,
		&run.TotalIssues,
		&run.QueuedIssues,
		&run.CompletedIssues,
		&run.FailedIssues,
		&paused,
		&run.CreatedAt,
		&run.UpdatedAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning backlog run: %w", err)
	}
	run.IncludeVariants = includeVariants == 1
	run.Paused = paused == 1
	return run, nil
}

func scanBacklogRunRow(rows *sql.Rows) (*model.BacklogRun, error) {
	run := &model.BacklogRun{}
	var includeVariants int
	var paused int
	if err := rows.Scan(
		&run.ID,
		&run.SeriesID,
		&run.SeriesTitle,
		&run.Status,
		&includeVariants,
		&run.TotalIssues,
		&run.QueuedIssues,
		&run.CompletedIssues,
		&run.FailedIssues,
		&paused,
		&run.CreatedAt,
		&run.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("scanning backlog run row: %w", err)
	}
	run.IncludeVariants = includeVariants == 1
	run.Paused = paused == 1
	return run, nil
}

func scanBacklogItem(row *sql.Row) (*model.BacklogItem, error) {
	item := &model.BacklogItem{}
	var downloadHistoryID sql.NullInt64
	var issueNumber sql.NullString
	var seriesTitle sql.NullString
	if err := row.Scan(
		&item.ID,
		&item.BacklogRunID,
		&item.SeriesID,
		&item.IssueID,
		&item.VariantName,
		&item.Priority,
		&item.Status,
		&item.RetryCount,
		&item.RetryAt,
		&item.LastError,
		&item.SabNzoID,
		&item.NZBGuid,
		&downloadHistoryID,
		&item.CreatedAt,
		&item.UpdatedAt,
		&issueNumber,
		&seriesTitle,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scanning backlog item: %w", err)
	}
	if downloadHistoryID.Valid {
		item.DownloadHistoryID = &downloadHistoryID.Int64
	}
	if issueNumber.Valid {
		item.IssueNumber = issueNumber.String
	}
	if seriesTitle.Valid {
		item.SeriesTitle = seriesTitle.String
	}
	return item, nil
}

func scanBacklogItemRow(rows *sql.Rows) (*model.BacklogItem, error) {
	item := &model.BacklogItem{}
	var downloadHistoryID sql.NullInt64
	var issueNumber sql.NullString
	var seriesTitle sql.NullString
	if err := rows.Scan(
		&item.ID,
		&item.BacklogRunID,
		&item.SeriesID,
		&item.IssueID,
		&item.VariantName,
		&item.Priority,
		&item.Status,
		&item.RetryCount,
		&item.RetryAt,
		&item.LastError,
		&item.SabNzoID,
		&item.NZBGuid,
		&downloadHistoryID,
		&item.CreatedAt,
		&item.UpdatedAt,
		&issueNumber,
		&seriesTitle,
	); err != nil {
		return nil, fmt.Errorf("scanning backlog item row: %w", err)
	}
	if downloadHistoryID.Valid {
		item.DownloadHistoryID = &downloadHistoryID.Int64
	}
	if issueNumber.Valid {
		item.IssueNumber = issueNumber.String
	}
	if seriesTitle.Valid {
		item.SeriesTitle = seriesTitle.String
	}
	return item, nil
}

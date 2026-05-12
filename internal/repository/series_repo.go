package repository

import (
	"database/sql"
	"fmt"
	"strings"
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
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id, s.metron_modified_at,
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

// ListByTitle returns every series whose title matches (case-insensitive),
// regardless of year. Used by the reattach pass to resolve "Daredevil"
// to the right volume when the strict (title, year) lookup misses
// because the orphan's parsed year doesn't match any local series year.
func (r *SeriesRepo) ListByTitle(title string) ([]model.Series, error) {
	rows, err := r.read.Query(`
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id, s.metron_modified_at,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			0 as issue_count, 0 as file_count, COALESCE(p.name, '') as publisher_name
		FROM series s
		LEFT JOIN publishers p ON s.publisher_id = p.id
		WHERE LOWER(s.title) = LOWER(?)
		ORDER BY s.id`, title)
	if err != nil {
		return nil, fmt.Errorf("listing series by title: %w", err)
	}
	defer rows.Close()
	var out []model.Series
	for rows.Next() {
		s, err := scanSeriesRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, nil
}

// FindByTitleAndYear finds a series by title and optional year.
func (r *SeriesRepo) FindByTitleAndYear(title string, year *int) (*model.Series, error) {
	var row *sql.Row
	if year != nil {
		row = r.read.QueryRow(`
			SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id, s.metron_modified_at,
				s.description, s.status, s.total_issues, s.cover_file_id, s.tracked,
				s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
				0 as issue_count, 0 as file_count, '' as publisher_name
			FROM series s
			WHERE LOWER(s.title) = LOWER(?) AND s.year = ?`, title, *year)
	} else {
		row = r.read.QueryRow(`
			SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id, s.metron_modified_at,
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
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id, s.metron_modified_at,
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
// ListWithoutPublisher returns every series whose publisher_id is NULL.
// Used by the publisher-backfill pass to know what to touch.
func (r *SeriesRepo) ListWithoutPublisher() ([]model.Series, error) {
	rows, err := r.read.Query(`
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id, s.metron_modified_at,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			0 as issue_count, 0 as file_count, '' as publisher_name
		FROM series s
		WHERE s.publisher_id IS NULL
		ORDER BY s.id`)
	if err != nil {
		return nil, fmt.Errorf("listing series without publisher: %w", err)
	}
	defer rows.Close()
	var out []model.Series
	for rows.Next() {
		s, err := scanSeriesRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, nil
}

// BackfillPublisherAndYear sets publisher_id and/or year on a series ONLY
// where those columns are currently NULL — never overwrites a value that
// metadata sync or the user already filled in.
func (r *SeriesRepo) BackfillPublisherAndYear(id int64, publisherID int64, year *int) error {
	_, err := r.write.Exec(`
		UPDATE series
		SET publisher_id = COALESCE(publisher_id, ?),
		    year         = COALESCE(year, ?),
		    updated_at   = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		WHERE id = ?`, publisherID, year, id)
	if err != nil {
		return fmt.Errorf("backfilling series publisher/year: %w", err)
	}
	return nil
}

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

// FindByMetronID finds a series by Metron ID. Mirrors FindByComicVineID.
// Returns (nil, nil) when no series carries the given Metron ID.
func (r *SeriesRepo) FindByMetronID(metronID int64) (*model.Series, error) {
	row := r.read.QueryRow(`
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id, s.metron_modified_at,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			COALESCE((SELECT COUNT(*) FROM issues WHERE series_id = s.id), 0) as issue_count,
			COALESCE((SELECT COUNT(*) FROM comic_files cf JOIN issues i ON cf.issue_id = i.id WHERE i.series_id = s.id), 0) as file_count,
			COALESCE(p.name, '') as publisher_name
		FROM series s
		LEFT JOIN publishers p ON s.publisher_id = p.id
		WHERE s.metron_id = ?`, metronID)
	return scanSeries(row)
}

// FindByComicVineID finds a series by ComicVine ID.
func (r *SeriesRepo) FindByComicVineID(cvID int64) (*model.Series, error) {
	row := r.read.QueryRow(`
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id, s.metron_modified_at,
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

// MergeStats summarizes what MergeInto did.
type MergeStats struct {
	IssuesRelocated   int `json:"issues_relocated"`   // source issues that had no target collision, re-parented to target
	IssuesConsolidated int `json:"issues_consolidated"` // source issues that collided with a target issue and were merged in
	FilesRepointed    int `json:"files_repointed"`
	WantListMerged    int `json:"want_list_merged"`
	StoryArcsMerged   int `json:"story_arcs_merged"`
	BacklogRepointed  int `json:"backlog_repointed"`
}

// MergeInto re-parents every child of `sourceSeriesID` under `targetSeriesID`
// and deletes the source row. Run inside a single SQLite write transaction
// so a failure mid-way rolls back without partial damage.
//
// Per-issue policy:
//   * Source issue whose (normalized) issue_number is NOT present in the
//     target → its series_id is flipped to target. comic_files /
//     want_list / story_arc / backlog FKs come along naturally.
//   * Source issue whose number IS present in target → re-point all FK
//     references from the source issue to the matching target issue,
//     handling the UNIQUE / PRIMARY KEY collisions on want_list and
//     story_arc_issues by dropping the source row when the target
//     already owns the link. The source issue is then deleted.
//
// Finally, the source series row is deleted. The CASCADE on issues
// would clean up anything we missed, but we never leave that to happen
// implicitly — every issue is dealt with before the parent series goes.
func (r *SeriesRepo) MergeInto(sourceSeriesID, targetSeriesID int64) (*MergeStats, error) {
	if sourceSeriesID == targetSeriesID {
		return nil, fmt.Errorf("source and target series must differ")
	}
	tx, err := r.write.Begin()
	if err != nil {
		return nil, fmt.Errorf("beginning merge tx: %w", err)
	}
	defer tx.Rollback()

	stats := &MergeStats{}

	// Build a (norm_number → target issue id) map up front.
	rows, err := tx.Query(`SELECT id, issue_number FROM issues WHERE series_id = ?`, targetSeriesID)
	if err != nil {
		return nil, fmt.Errorf("listing target issues: %w", err)
	}
	type targetIssue struct {
		id     int64
		number string
	}
	targetByNum := make(map[string]targetIssue)
	for rows.Next() {
		var ti targetIssue
		if err := rows.Scan(&ti.id, &ti.number); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scanning target issue: %w", err)
		}
		targetByNum[mergeNormalizeIssueNumber(ti.number)] = ti
	}
	rows.Close()

	// Walk source issues.
	srcRows, err := tx.Query(`SELECT id, issue_number FROM issues WHERE series_id = ?`, sourceSeriesID)
	if err != nil {
		return nil, fmt.Errorf("listing source issues: %w", err)
	}
	type srcIssue struct {
		id     int64
		number string
	}
	var sources []srcIssue
	for srcRows.Next() {
		var s srcIssue
		if err := srcRows.Scan(&s.id, &s.number); err != nil {
			srcRows.Close()
			return nil, fmt.Errorf("scanning source issue: %w", err)
		}
		sources = append(sources, s)
	}
	srcRows.Close()

	for _, src := range sources {
		key := mergeNormalizeIssueNumber(src.number)
		tgt, exists := targetByNum[key]
		if !exists {
			// Relocate the whole issue under target.
			if _, err := tx.Exec(`UPDATE issues SET series_id = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = ?`, targetSeriesID, src.id); err != nil {
				return nil, fmt.Errorf("relocating issue %d: %w", src.id, err)
			}
			targetByNum[key] = targetIssue{id: src.id, number: src.number}
			stats.IssuesRelocated++
			continue
		}

		// Consolidate: re-point all references from src.id → tgt.id.
		if r, err := tx.Exec(`UPDATE comic_files SET issue_id = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE issue_id = ?`, tgt.id, src.id); err == nil {
			if n, _ := r.RowsAffected(); n > 0 {
				stats.FilesRepointed += int(n)
			}
		} else {
			return nil, fmt.Errorf("repointing comic_files src=%d tgt=%d: %w", src.id, tgt.id, err)
		}

		// want_list (UNIQUE on issue_id) — drop conflicting source rows first.
		if _, err := tx.Exec(`
			DELETE FROM want_list WHERE issue_id = ?
			  AND EXISTS (SELECT 1 FROM want_list w2 WHERE w2.issue_id = ?)`, src.id, tgt.id); err != nil {
			return nil, fmt.Errorf("pre-merge want_list dedupe: %w", err)
		}
		if r, err := tx.Exec(`UPDATE want_list SET issue_id = ? WHERE issue_id = ?`, tgt.id, src.id); err == nil {
			if n, _ := r.RowsAffected(); n > 0 {
				stats.WantListMerged += int(n)
			}
		} else {
			return nil, fmt.Errorf("repointing want_list: %w", err)
		}

		// story_arc_issues (PK(story_arc_id, issue_id)) — drop where tgt already linked.
		if _, err := tx.Exec(`
			DELETE FROM story_arc_issues
			WHERE issue_id = ?
			  AND EXISTS (SELECT 1 FROM story_arc_issues s2
			              WHERE s2.story_arc_id = story_arc_issues.story_arc_id
			                AND s2.issue_id = ?)`, src.id, tgt.id); err != nil {
			return nil, fmt.Errorf("pre-merge story_arc_issues dedupe: %w", err)
		}
		if r, err := tx.Exec(`UPDATE story_arc_issues SET issue_id = ? WHERE issue_id = ?`, tgt.id, src.id); err == nil {
			if n, _ := r.RowsAffected(); n > 0 {
				stats.StoryArcsMerged += int(n)
			}
		} else {
			return nil, fmt.Errorf("repointing story_arc_issues: %w", err)
		}

		// backlog_items (no UNIQUE) — straight UPDATE.
		if r, err := tx.Exec(`UPDATE backlog_items SET issue_id = ? WHERE issue_id = ?`, tgt.id, src.id); err == nil {
			if n, _ := r.RowsAffected(); n > 0 {
				stats.BacklogRepointed += int(n)
			}
		} else {
			return nil, fmt.Errorf("repointing backlog_items: %w", err)
		}

		// download_history (no UNIQUE either).
		if _, err := tx.Exec(`UPDATE download_history SET issue_id = ? WHERE issue_id = ?`, tgt.id, src.id); err != nil {
			return nil, fmt.Errorf("repointing download_history: %w", err)
		}

		// Source issue can go now that nothing references it.
		if _, err := tx.Exec(`DELETE FROM issues WHERE id = ?`, src.id); err != nil {
			return nil, fmt.Errorf("deleting source issue %d: %w", src.id, err)
		}
		stats.IssuesConsolidated++
	}

	// Re-parent annual-series rows.
	if _, err := tx.Exec(`UPDATE series SET parent_series_id = ? WHERE parent_series_id = ?`, targetSeriesID, sourceSeriesID); err != nil {
		return nil, fmt.Errorf("re-parenting annuals: %w", err)
	}

	// Delete the source series row. (CASCADE would only fire on issues
	// not yet handled — there should be none.)
	if _, err := tx.Exec(`DELETE FROM series WHERE id = ?`, sourceSeriesID); err != nil {
		return nil, fmt.Errorf("deleting source series: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("committing merge: %w", err)
	}
	return stats, nil
}

// mergeNormalizeIssueNumber strips leading zeros so "001" and "1" collide.
// Mirrors the helper used by the calendar / reattach paths.
func mergeNormalizeIssueNumber(n string) string {
	n = strings.TrimSpace(n)
	if n == "" {
		return ""
	}
	bare := strings.TrimLeft(n, "0")
	if bare == "" {
		bare = "0"
	}
	return strings.ToLower(bare)
}

// SetMetronID stores Metron's series ID + its modified-at timestamp on a
// local series row. Idempotent — calling twice with the same value is a
// no-op. metron_id is UNIQUE so attempting to set the same id on two
// rows surfaces as an SQLite UNIQUE-constraint error to the caller.
func (r *SeriesRepo) SetMetronID(id, metronID int64, modifiedAt string) error {
	_, err := r.write.Exec(`
		UPDATE series
		SET metron_id = ?, metron_modified_at = ?,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		WHERE id = ?`, metronID, modifiedAt, id)
	if err != nil {
		return fmt.Errorf("setting metron_id: %w", err)
	}
	return nil
}

// FillDescriptionIfEmpty writes the supplied description ONLY if the
// existing series.description column is empty/NULL. Used by the
// metadata-enrich pass to fill in Metron data without clobbering CV
// descriptions that the user may already be happy with.
func (r *SeriesRepo) FillDescriptionIfEmpty(id int64, desc string) error {
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return nil
	}
	_, err := r.write.Exec(`
		UPDATE series
		SET description = ?,
		    updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')
		WHERE id = ? AND (description IS NULL OR description = '')`, desc, id)
	if err != nil {
		return fmt.Errorf("filling series description: %w", err)
	}
	return nil
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
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id, s.metron_modified_at,
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
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id, s.metron_modified_at,
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
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id, s.metron_modified_at,
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
		&s.ID, &s.Title, &s.SortTitle, &s.Year, &s.PublisherID, &s.ComicVineID, &s.MetronID, &s.MetronModified,
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
		&s.ID, &s.Title, &s.SortTitle, &s.Year, &s.PublisherID, &s.ComicVineID, &s.MetronID, &s.MetronModified,
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

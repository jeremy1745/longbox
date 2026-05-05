package repository

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/scanner"
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
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, COALESCE(s.cover_image_url,''), s.tracked,
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
//
// Match is done against sort_title (which idx_series_title indexes) instead
// of `LOWER(title)` — the latter forces a full table scan because there's
// no functional index on it. sort_title is already lowercase-normalized
// via scanner.MakeSortTitle and hits the index. On a 5k-series library
// this turns a per-file linear scan during library import into a B-tree
// lookup.
func (r *SeriesRepo) FindByTitleAndYear(title string, year *int) (*model.Series, error) {
	sortTitle := scanner.MakeSortTitle(title)
	var row *sql.Row
	if year != nil {
		row = r.read.QueryRow(`
			SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id,
				s.description, s.status, s.total_issues, s.cover_file_id, COALESCE(s.cover_image_url,''), s.tracked,
				s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
				0 as issue_count, 0 as file_count, '' as publisher_name
			FROM series s
			WHERE s.sort_title = ? AND s.year = ?`, sortTitle, *year)
	} else {
		row = r.read.QueryRow(`
			SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id,
				s.description, s.status, s.total_issues, s.cover_file_id, COALESCE(s.cover_image_url,''), s.tracked,
				s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
				0 as issue_count, 0 as file_count, '' as publisher_name
			FROM series s
			WHERE s.sort_title = ? AND s.year IS NULL`, sortTitle)
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

	// Replaces the prior correlated `(SELECT COUNT(*) ...)` per-row pattern
	// with two grouped LEFT JOINs. The optimizer evaluates each group-by
	// once per query rather than N times. On a 5k-series library this
	// turns a 1–2s page render into <50ms.
	query := fmt.Sprintf(`
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, COALESCE(s.cover_image_url,''), s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			COALESCE(ic.c, 0) as issue_count,
			COALESCE(fc.c, 0) as file_count,
			COALESCE(p.name, '') as publisher_name
		FROM series s
		LEFT JOIN publishers p ON s.publisher_id = p.id
		LEFT JOIN (SELECT series_id, COUNT(*) c FROM issues GROUP BY series_id) ic ON ic.series_id = s.id
		LEFT JOIN (SELECT i.series_id, COUNT(*) c FROM comic_files cf JOIN issues i ON cf.issue_id = i.id GROUP BY i.series_id) fc ON fc.series_id = s.id
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
			cover_image_url = COALESCE(NULLIF(?, ''), cover_image_url),
			last_cv_sync = ?, updated_at = ?
		WHERE id = ?`,
		s.Title, s.SortTitle, s.Year, s.PublisherID, s.ComicVineID,
		s.Description, s.Status, s.TotalIssues, s.CoverImageURL,
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339), s.ID,
	)
	return err
}

// UpdateFromMetronMetadata updates a series with metadata sourced from
// Metron. Like UpdateFromMetadata but writes metron_id and (optionally)
// cross-references the comicvine_id when Metron carries one.
func (r *SeriesRepo) UpdateFromMetronMetadata(s *model.Series) error {
	_, err := r.write.Exec(`
		UPDATE series SET title = ?, sort_title = ?, year = ?, publisher_id = ?,
			metron_id = ?, comicvine_id = COALESCE(?, comicvine_id),
			description = ?, status = ?, total_issues = ?,
			cover_image_url = COALESCE(NULLIF(?, ''), cover_image_url),
			last_cv_sync = ?, updated_at = ?
		WHERE id = ?`,
		s.Title, s.SortTitle, s.Year, s.PublisherID, s.MetronID, s.ComicVineID,
		s.Description, s.Status, s.TotalIssues, s.CoverImageURL,
		time.Now().UTC().Format(time.RFC3339),
		time.Now().UTC().Format(time.RFC3339), s.ID,
	)
	return err
}

// SetSeriesCoverImageURL writes only the cover_image_url column. Used when
// a metadata provider supplies a cover URL out-of-band from the main series
// update (e.g., picking it from the first Metron issue's image).
func (r *SeriesRepo) SetSeriesCoverImageURL(id int64, url string) error {
	_, err := r.write.Exec(
		`UPDATE series SET cover_image_url = ?, updated_at = ? WHERE id = ?`,
		url, time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// SetMetronModifiedAt records the Last-Modified header from the most recent
// Metron detail fetch. Subsequent refreshes send this value as
// If-Modified-Since so unchanged resources return 304 without burning quota.
func (r *SeriesRepo) SetMetronModifiedAt(id int64, lastModified string) error {
	_, err := r.write.Exec(
		`UPDATE series SET metron_modified_at = ?, updated_at = ? WHERE id = ?`,
		lastModified, time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// GetMetronModifiedAt returns the saved Last-Modified value (empty if never
// fetched).
func (r *SeriesRepo) GetMetronModifiedAt(id int64) (string, error) {
	var v sql.NullString
	if err := r.read.QueryRow(`SELECT metron_modified_at FROM series WHERE id = ?`, id).Scan(&v); err != nil {
		return "", err
	}
	return v.String, nil
}

// SetMetronID writes only the metron_id column (used to opportunistically
// cross-link an already-CV-matched series to Metron without touching other
// fields).
func (r *SeriesRepo) SetMetronID(id int64, metronID *int64) error {
	_, err := r.write.Exec(
		`UPDATE series SET metron_id = ?, updated_at = ? WHERE id = ?`,
		metronID, time.Now().UTC().Format(time.RFC3339), id,
	)
	return err
}

// FindByMetronID finds a series by Metron ID.
func (r *SeriesRepo) FindByMetronID(metronID int64) (*model.Series, error) {
	row := r.read.QueryRow(`
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, COALESCE(s.cover_image_url,''), s.tracked,
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
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, COALESCE(s.cover_image_url,''), s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			COALESCE((SELECT COUNT(*) FROM issues WHERE series_id = s.id), 0) as issue_count,
			COALESCE((SELECT COUNT(*) FROM comic_files cf JOIN issues i ON cf.issue_id = i.id WHERE i.series_id = s.id), 0) as file_count,
			COALESCE(p.name, '') as publisher_name
		FROM series s
		LEFT JOIN publishers p ON s.publisher_id = p.id
		WHERE s.comicvine_id = ?`, cvID)
	return scanSeries(row)
}

// DuplicateSeriesGroup names a (normalized_title, year) pair that has more
// than one row in the series table.
type DuplicateSeriesGroup struct {
	NormalizedTitle string
	Year            *int
	SeriesIDs       []int64
}

// normalizeSeriesKey produces an aggressive grouping key for series
// dedupe: lowercased, all non-alphanumeric stripped. Collapses
// punctuation differences ("All-New Spider-Gwen: The Ghost-Spider" vs
// "All-New Spider-Gwen The Ghost-Spider"), whitespace differences
// ("Fantastic Four " vs "Fantastic Four"), and case differences. Loose
// enough that we only use it as the GROUPING bucket; the actual merge
// canonical pick still goes through PickCanonicalSeriesID which prefers
// CV-matched + most-files rows.
func normalizeSeriesKey(title string) string {
	var b strings.Builder
	b.Grow(len(title))
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// FindDuplicateSeriesGroups returns every group of series rows that should
// merge into one canonical row. Catches three patterns:
//
//   1. **Exact match:** same normalized title + same year → multiple rows.
//      Classic dupe (parsed twice from different filenames).
//   2. **Year-NULL ghost:** "Foo" (year IS NULL, no provider match) AND
//      "Foo (2024)" (year set, matched). The NULL row is almost always
//      a filename-parsed placeholder that never got matched; merging it
//      into the matched row is safe and recovers any orphan files.
//   3. **Multiple years + a NULL ghost:** "Foo", "Foo (2023)", "Foo (2024)"
//      all coexist. Pattern 2 logic still applies — the NULL row gets
//      pulled into ALL year-set neighbors via separate group entries
//      (one merge target per year), but in practice the canonical pick
//      sorts by file count + match status so the dominant year wins.
//
// Year-set rows with DIFFERENT years are intentionally NOT grouped (they
// may be legitimate distinct volumes — "Wonder Man 2007" vs "Wonder Man
// 2024"). To collapse those, the user uses the per-series Merge Into
// action.
func (r *SeriesRepo) FindDuplicateSeriesGroups() ([]DuplicateSeriesGroup, error) {
	// Single in-Go pass with strict normalization.
	// Beats the prior multi-query SQL approach because SQL `LOWER(TRIM(...))`
	// can't strip punctuation/articles consistently — series with subtle
	// differences ("Foo: Bar" vs "Foo Bar", "Fantastic Four " vs
	// "Fantastic Four") slipped through.
	rows, err := r.read.Query(`
		SELECT id, title, year, comicvine_id
		FROM series
	`)
	if err != nil {
		return nil, fmt.Errorf("listing series for dedupe: %w", err)
	}
	defer rows.Close()

	type seriesEntry struct {
		id          int64
		title       string
		year        sql.NullInt64
		comicvineID sql.NullInt64
	}
	byKey := make(map[string][]seriesEntry)
	for rows.Next() {
		var e seriesEntry
		if err := rows.Scan(&e.id, &e.title, &e.year, &e.comicvineID); err != nil {
			return nil, fmt.Errorf("scanning series for dedupe: %w", err)
		}
		key := normalizeSeriesKey(e.title)
		if key == "" {
			continue
		}
		byKey[key] = append(byKey[key], e)
	}

	var groups []DuplicateSeriesGroup
	for key, entries := range byKey {
		if len(entries) < 2 {
			continue
		}

		// Count CV-matched rows in this key bucket. Decision rule:
		//   - Exactly one CV match → fold the unmatched siblings into it.
		//     Safe — true volume splits ("Wonder Man 2007" vs "Wonder Man
		//     2024") would have distinct CV matches and produce ≥2 here.
		//   - Zero CV matches → fold the year-NULL ghost into ANY year-set
		//     sibling. The year-NULL row is a filename-parsed placeholder.
		//   - ≥2 CV matches → likely legitimate distinct volumes; do NOT
		//     auto-merge. User can resolve via per-series Merge Into.
		matched := 0
		var hasNullYear bool
		for _, e := range entries {
			if e.comicvineID.Valid {
				matched++
			}
			if !e.year.Valid {
				hasNullYear = true
			}
		}

		group := DuplicateSeriesGroup{NormalizedTitle: key}
		switch {
		case matched == 1:
			// Fold every same-key sibling into the matched row.
			for _, e := range entries {
				group.SeriesIDs = append(group.SeriesIDs, e.id)
			}
		case matched == 0 && hasNullYear:
			// Pull the NULL row(s) into ANY year-set sibling. Take the
			// first year-set entry as canonical; if multiple year-set
			// rows exist with no CV match we leave them alone (could be
			// real volumes; without metadata we can't tell).
			var nullIDs, yearIDs []int64
			for _, e := range entries {
				if e.year.Valid {
					yearIDs = append(yearIDs, e.id)
				} else {
					nullIDs = append(nullIDs, e.id)
				}
			}
			if len(yearIDs) >= 1 && len(nullIDs) >= 1 {
				// One group per year-set sibling × null. After the first
				// merge runs, the null row is gone; subsequent groups
				// no-op via PickCanonicalSeriesID's deleted-id handling.
				for _, yid := range yearIDs {
					g := DuplicateSeriesGroup{NormalizedTitle: key}
					g.SeriesIDs = append(g.SeriesIDs, yid)
					g.SeriesIDs = append(g.SeriesIDs, nullIDs...)
					if len(g.SeriesIDs) > 1 {
						groups = append(groups, g)
					}
				}
				continue
			}
			// All NULL years (no year-set sibling) → exact-duplicate
			// grouping; merge them all together.
			for _, e := range entries {
				group.SeriesIDs = append(group.SeriesIDs, e.id)
			}
		default:
			// matched >= 2 OR (matched==0 AND no NULL): treat as exact
			// duplicates only when the YEARS match too. Otherwise
			// abstain — could be legitimate volume splits.
			byYear := make(map[int64][]int64)
			for _, e := range entries {
				yk := int64(-1)
				if e.year.Valid {
					yk = e.year.Int64
				}
				byYear[yk] = append(byYear[yk], e.id)
			}
			for _, ids := range byYear {
				if len(ids) > 1 {
					g := DuplicateSeriesGroup{NormalizedTitle: key, SeriesIDs: ids}
					groups = append(groups, g)
				}
			}
			continue
		}
		if len(group.SeriesIDs) > 1 {
			groups = append(groups, group)
		}
	}
	return groups, nil
}

// findExactDuplicateGroups: pattern 1 — same title + same year.
func (r *SeriesRepo) findExactDuplicateGroups() ([]DuplicateSeriesGroup, error) {
	rows, err := r.read.Query(`
		SELECT LOWER(TRIM(title)) AS norm, year, GROUP_CONCAT(id)
		FROM series
		GROUP BY norm, year
		HAVING COUNT(*) > 1
	`)
	if err != nil {
		return nil, fmt.Errorf("listing exact duplicate series groups: %w", err)
	}
	defer rows.Close()

	var groups []DuplicateSeriesGroup
	for rows.Next() {
		var g DuplicateSeriesGroup
		var ids string
		var year sql.NullInt64
		if err := rows.Scan(&g.NormalizedTitle, &year, &ids); err != nil {
			return nil, fmt.Errorf("scanning duplicate series group: %w", err)
		}
		if year.Valid {
			y := int(year.Int64)
			g.Year = &y
		}
		for _, idStr := range strings.Split(ids, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
			if err != nil {
				continue
			}
			g.SeriesIDs = append(g.SeriesIDs, id)
		}
		if len(g.SeriesIDs) > 1 {
			groups = append(groups, g)
		}
	}
	return groups, nil
}

// findUnmatchedSiblingGroups: pattern 4 — same normalized title across
// multiple rows where exactly ONE row is matched to ComicVine and the
// rest are not. The unmatched siblings are almost always filename-parsed
// ghosts (`Fantastic Four`, `Fantastic Four (2023)`, `Fantastic Four
// (2024)` all coexist next to a single CV-matched `Fantastic Four
// (2026)`). Merging the unmatched into the matched is safe — true
// volume splits like "Wonder Man 2007" vs "Wonder Man 2024" both have
// distinct CV matches and won't be touched. Returns one group containing
// the matched row + every unmatched same-titled row.
func (r *SeriesRepo) findUnmatchedSiblingGroups() ([]DuplicateSeriesGroup, error) {
	rows, err := r.read.Query(`
		WITH matched AS (
			SELECT LOWER(TRIM(title)) AS norm, id, year
			FROM series
			WHERE comicvine_id IS NOT NULL
		),
		title_match_counts AS (
			SELECT norm, COUNT(*) AS n FROM matched GROUP BY norm
		)
		SELECT m.norm, m.year,
		       m.id || ',' || COALESCE(GROUP_CONCAT(u.id), '') AS ids
		FROM matched m
		JOIN title_match_counts c ON c.norm = m.norm AND c.n = 1
		LEFT JOIN series u ON LOWER(TRIM(u.title)) = m.norm AND u.comicvine_id IS NULL
		GROUP BY m.id
		HAVING GROUP_CONCAT(u.id) IS NOT NULL
	`)
	if err != nil {
		return nil, fmt.Errorf("listing unmatched-sibling groups: %w", err)
	}
	defer rows.Close()

	var groups []DuplicateSeriesGroup
	for rows.Next() {
		var g DuplicateSeriesGroup
		var year sql.NullInt64
		var ids string
		if err := rows.Scan(&g.NormalizedTitle, &year, &ids); err != nil {
			return nil, fmt.Errorf("scanning unmatched-sibling group: %w", err)
		}
		if year.Valid {
			y := int(year.Int64)
			g.Year = &y
		}
		seen := make(map[int64]bool)
		for _, idStr := range strings.Split(ids, ",") {
			idStr = strings.TrimSpace(idStr)
			if idStr == "" {
				continue
			}
			id, err := strconv.ParseInt(idStr, 10, 64)
			if err != nil || seen[id] {
				continue
			}
			seen[id] = true
			g.SeriesIDs = append(g.SeriesIDs, id)
		}
		if len(g.SeriesIDs) > 1 {
			groups = append(groups, g)
		}
	}
	return groups, nil
}

// findYearNullGhostGroups: pattern 2/3 — when a series row exists with
// year=NULL AND ≥1 same-titled row exists with year set, collapse them.
// Returns one group per year-set neighbor with the NULL row as a member,
// so each merge can run via the existing PickCanonicalSeriesID flow
// (which prefers the year-set + provider-matched row). The same NULL row
// appearing in multiple groups is fine — once merged into the first
// canonical, subsequent groups are no-ops because the source ID is gone.
func (r *SeriesRepo) findYearNullGhostGroups() ([]DuplicateSeriesGroup, error) {
	rows, err := r.read.Query(`
		WITH null_titles AS (
			SELECT LOWER(TRIM(title)) AS norm, GROUP_CONCAT(id) AS null_ids
			FROM series
			WHERE year IS NULL
			GROUP BY norm
		),
		year_titles AS (
			SELECT LOWER(TRIM(title)) AS norm, year, GROUP_CONCAT(id) AS year_ids
			FROM series
			WHERE year IS NOT NULL
			GROUP BY norm, year
		)
		SELECT n.norm, y.year, n.null_ids, y.year_ids
		FROM null_titles n
		JOIN year_titles y ON y.norm = n.norm
	`)
	if err != nil {
		return nil, fmt.Errorf("listing year-null ghost groups: %w", err)
	}
	defer rows.Close()

	var groups []DuplicateSeriesGroup
	for rows.Next() {
		var g DuplicateSeriesGroup
		var year sql.NullInt64
		var nullIDs, yearIDs string
		if err := rows.Scan(&g.NormalizedTitle, &year, &nullIDs, &yearIDs); err != nil {
			return nil, fmt.Errorf("scanning ghost group: %w", err)
		}
		if year.Valid {
			y := int(year.Int64)
			g.Year = &y
		}
		seen := make(map[int64]bool)
		for _, idStr := range append(strings.Split(nullIDs, ","), strings.Split(yearIDs, ",")...) {
			id, err := strconv.ParseInt(strings.TrimSpace(idStr), 10, 64)
			if err != nil || seen[id] {
				continue
			}
			seen[id] = true
			g.SeriesIDs = append(g.SeriesIDs, id)
		}
		if len(g.SeriesIDs) > 1 {
			groups = append(groups, g)
		}
	}
	return groups, nil
}

// PickCanonicalSeriesID picks the preferred canonical series row from a
// duplicate group. Preference: has comicvine_id, then metron_id, then most
// file_count, then lowest id. Returns canonical + the rest.
func (r *SeriesRepo) PickCanonicalSeriesID(ids []int64) (int64, []int64, error) {
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
		SELECT s.id FROM series s
		WHERE s.id IN (%s)
		ORDER BY (CASE WHEN s.comicvine_id IS NOT NULL THEN 0 ELSE 1 END),
			(CASE WHEN s.metron_id IS NOT NULL THEN 0 ELSE 1 END),
			(SELECT COUNT(*) FROM comic_files cf
			   JOIN issues i ON cf.issue_id = i.id
			   WHERE i.series_id = s.id) DESC,
			s.id ASC
	`, placeholders), args...)
	if err != nil {
		return 0, nil, fmt.Errorf("ranking series ids: %w", err)
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

// Delete removes a series row. Caller is responsible for ensuring no issues
// or comic_files rows reference it (the schema does not cascade).
func (r *SeriesRepo) Delete(id int64) error {
	_, err := r.write.Exec(`DELETE FROM series WHERE id = ?`, id)
	return err
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
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, COALESCE(s.cover_image_url,''), s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			COALESCE(ic.c, 0) as issue_count,
			COALESCE(fc.c, 0) as file_count,
			COALESCE(p.name, '') as publisher_name
		FROM series s
		LEFT JOIN publishers p ON s.publisher_id = p.id
		LEFT JOIN (SELECT series_id, COUNT(*) c FROM issues GROUP BY series_id) ic ON ic.series_id = s.id
		LEFT JOIN (SELECT i.series_id, COUNT(*) c FROM comic_files cf JOIN issues i ON cf.issue_id = i.id GROUP BY i.series_id) fc ON fc.series_id = s.id
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
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, COALESCE(s.cover_image_url,''), s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			COALESCE(ic.c, 0) as issue_count,
			COALESCE(fc.c, 0) as file_count,
			COALESCE(p.name, '') as publisher_name
		FROM series s
		LEFT JOIN publishers p ON s.publisher_id = p.id
		LEFT JOIN (SELECT series_id, COUNT(*) c FROM issues GROUP BY series_id) ic ON ic.series_id = s.id
		LEFT JOIN (SELECT i.series_id, COUNT(*) c FROM comic_files cf JOIN issues i ON cf.issue_id = i.id GROUP BY i.series_id) fc ON fc.series_id = s.id
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

func (r *SeriesRepo) ClearCoverFileID(seriesID int64) error {
	_, err := r.write.Exec(`UPDATE series SET cover_file_id = NULL, updated_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), seriesID)
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
		SELECT s.id, s.title, s.sort_title, s.year, s.publisher_id, s.comicvine_id, s.metron_id,
			COALESCE(s.description,''), s.status, s.total_issues, s.cover_file_id, COALESCE(s.cover_image_url,''), s.tracked,
			s.metadata_locked, s.last_cv_sync, s.parent_series_id, s.created_at, s.updated_at,
			COALESCE(ic.c, 0) as issue_count,
			COALESCE(fc.c, 0) as file_count,
			COALESCE(p.name, '') as publisher_name
		FROM series s
		LEFT JOIN publishers p ON s.publisher_id = p.id
		LEFT JOIN (SELECT series_id, COUNT(*) c FROM issues GROUP BY series_id) ic ON ic.series_id = s.id
		LEFT JOIN (SELECT i.series_id, COUNT(*) c FROM comic_files cf JOIN issues i ON cf.issue_id = i.id GROUP BY i.series_id) fc ON fc.series_id = s.id
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
		&s.ID, &s.Title, &s.SortTitle, &s.Year, &s.PublisherID, &s.ComicVineID, &s.MetronID,
		&s.Description, &s.Status, &s.TotalIssues, &s.CoverFileID, &s.CoverImageURL, &s.Tracked,
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
		&s.ID, &s.Title, &s.SortTitle, &s.Year, &s.PublisherID, &s.ComicVineID, &s.MetronID,
		&s.Description, &s.Status, &s.TotalIssues, &s.CoverFileID, &s.CoverImageURL, &s.Tracked,
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

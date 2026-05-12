package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jeremy/longbox/internal/model"
)

type FileRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewFileRepo(read, write *sql.DB) *FileRepo {
	return &FileRepo{read: read, write: write}
}

func (r *FileRepo) Create(f *model.ComicFile) error {
	res, err := r.write.Exec(`
		INSERT INTO comic_files (issue_id, file_path, file_name, file_size, file_hash, file_format, page_count,
			has_comicinfo, cover_path, parsed_series, parsed_number, parsed_year, match_confidence)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.IssueID, f.FilePath, f.FileName, f.FileSize, f.FileHash, f.FileFormat, f.PageCount,
		f.HasComicInfo, f.CoverPath, f.ParsedSeries, f.ParsedNumber, f.ParsedYear, f.MatchConfidence,
	)
	if err != nil {
		return fmt.Errorf("inserting comic file: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting last insert id: %w", err)
	}
	f.ID = id
	return nil
}

func (r *FileRepo) GetByID(id int64) (*model.ComicFile, error) {
	row := r.read.QueryRow(`SELECT id, issue_id, file_path, file_name, file_size, file_hash,
		file_format, page_count, has_comicinfo, cover_path, parsed_series, parsed_number,
		parsed_year, match_confidence, created_at, updated_at
		FROM comic_files WHERE id = ?`, id)
	return scanComicFile(row)
}

func (r *FileRepo) GetByPath(path string) (*model.ComicFile, error) {
	row := r.read.QueryRow(`SELECT id, issue_id, file_path, file_name, file_size, file_hash,
		file_format, page_count, has_comicinfo, cover_path, parsed_series, parsed_number,
		parsed_year, match_confidence, created_at, updated_at
		FROM comic_files WHERE file_path = ?`, path)
	return scanComicFile(row)
}

func (r *FileRepo) ExistsByPath(path string) (bool, error) {
	var count int
	err := r.read.QueryRow(`SELECT COUNT(*) FROM comic_files WHERE file_path = ?`, path).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking file existence: %w", err)
	}
	return count > 0, nil
}

func (r *FileRepo) List(page, perPage int) ([]model.ComicFile, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	}
	offset := (page - 1) * perPage

	var total int
	if err := r.read.QueryRow(`SELECT COUNT(*) FROM comic_files`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting files: %w", err)
	}

	rows, err := r.read.Query(`SELECT id, issue_id, file_path, file_name, file_size, file_hash,
		file_format, page_count, has_comicinfo, cover_path, parsed_series, parsed_number,
		parsed_year, match_confidence, created_at, updated_at
		FROM comic_files ORDER BY file_name ASC LIMIT ? OFFSET ?`, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing files: %w", err)
	}
	defer rows.Close()

	var files []model.ComicFile
	for rows.Next() {
		f, err := scanComicFileRow(rows)
		if err != nil {
			return nil, 0, err
		}
		files = append(files, *f)
	}
	return files, total, nil
}

func (r *FileRepo) UpdateCoverPath(id int64, coverPath string) error {
	_, err := r.write.Exec(`UPDATE comic_files SET cover_path = ?, updated_at = ? WHERE id = ?`,
		coverPath, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// UpdatePath updates the file path and name after a file move/rename.
func (r *FileRepo) UpdatePath(id int64, filePath, fileName string) error {
	_, err := r.write.Exec(`UPDATE comic_files SET file_path = ?, file_name = ?, updated_at = ? WHERE id = ?`,
		filePath, fileName, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// UpdateIssueID links a comic file to an issue.
func (r *FileRepo) UpdateIssueID(id int64, issueID int64) error {
	_, err := r.write.Exec(`UPDATE comic_files SET issue_id = ?, match_confidence = 1.0, updated_at = ? WHERE id = ?`,
		issueID, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// ListAll returns all comic files (no pagination). Used for bulk operations like organize.
func (r *FileRepo) ListAll() ([]model.ComicFile, error) {
	rows, err := r.read.Query(`SELECT id, issue_id, file_path, file_name, file_size, file_hash,
		file_format, page_count, has_comicinfo, cover_path, parsed_series, parsed_number,
		parsed_year, match_confidence, created_at, updated_at
		FROM comic_files ORDER BY file_name ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing all files: %w", err)
	}
	defer rows.Close()

	var files []model.ComicFile
	for rows.Next() {
		f, err := scanComicFileRow(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, *f)
	}
	return files, nil
}

// GetByIssueID returns the comic file linked to the given issue, if any.
func (r *FileRepo) GetByIssueID(issueID int64) (*model.ComicFile, error) {
	row := r.read.QueryRow(`SELECT id, issue_id, file_path, file_name, file_size, file_hash,
		file_format, page_count, has_comicinfo, cover_path, parsed_series, parsed_number,
		parsed_year, match_confidence, created_at, updated_at
		FROM comic_files WHERE issue_id = ? LIMIT 1`, issueID)
	return scanComicFile(row)
}

// ListBySeries returns all comic files for issues in the given series.
func (r *FileRepo) ListBySeries(seriesID int64) ([]model.ComicFile, error) {
	rows, err := r.read.Query(`
		SELECT cf.id, cf.issue_id, cf.file_path, cf.file_name, cf.file_size, cf.file_hash,
			cf.file_format, cf.page_count, cf.has_comicinfo, cf.cover_path, cf.parsed_series,
			cf.parsed_number, cf.parsed_year, cf.match_confidence, cf.created_at, cf.updated_at
		FROM comic_files cf
		JOIN issues i ON i.id = cf.issue_id
		WHERE i.series_id = ?
		ORDER BY i.sort_number ASC`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("listing files by series: %w", err)
	}
	defer rows.Close()

	var files []model.ComicFile
	for rows.Next() {
		f, err := scanComicFileRow(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, *f)
	}
	return files, nil
}

// Search returns comic files matching the query (case-insensitive file_name LIKE), with pagination.
func (r *FileRepo) Search(query string, page, perPage int) ([]model.ComicFile, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	}
	offset := (page - 1) * perPage
	pattern := "%" + query + "%"

	var total int
	if err := r.read.QueryRow(`SELECT COUNT(*) FROM comic_files WHERE file_name LIKE ? COLLATE NOCASE`, pattern).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting search results: %w", err)
	}

	rows, err := r.read.Query(`SELECT id, issue_id, file_path, file_name, file_size, file_hash,
		file_format, page_count, has_comicinfo, cover_path, parsed_series, parsed_number,
		parsed_year, match_confidence, created_at, updated_at
		FROM comic_files WHERE file_name LIKE ? COLLATE NOCASE ORDER BY file_name ASC LIMIT ? OFFSET ?`, pattern, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("searching files: %w", err)
	}
	defer rows.Close()

	var files []model.ComicFile
	for rows.Next() {
		f, err := scanComicFileRow(rows)
		if err != nil {
			return nil, 0, err
		}
		files = append(files, *f)
	}
	return files, total, nil
}

// UpdateHasComicInfo updates the has_comicinfo flag after writing metadata.
func (r *FileRepo) UpdateHasComicInfo(id int64, hasComicInfo bool) error {
	_, err := r.write.Exec(`UPDATE comic_files SET has_comicinfo = ?, updated_at = ? WHERE id = ?`,
		hasComicInfo, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

func (r *FileRepo) Delete(id int64) error {
	_, err := r.write.Exec(`DELETE FROM comic_files WHERE id = ?`, id)
	return err
}

// UpdateHash sets the file_hash for a comic file.
func (r *FileRepo) UpdateHash(id int64, hash string) error {
	_, err := r.write.Exec(`UPDATE comic_files SET file_hash = ?, updated_at = ? WHERE id = ?`,
		hash, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// DuplicateGroup represents a set of files sharing the same hash or issue.
type DuplicateGroup struct {
	Key   string          `json:"key"`
	Files []model.ComicFile `json:"files"`
}

// FindDuplicatesByHash returns groups of files with the same non-empty file_hash.
func (r *FileRepo) FindDuplicatesByHash() ([]DuplicateGroup, error) {
	rows, err := r.read.Query(`
		SELECT id, issue_id, file_path, file_name, file_size, file_hash,
			file_format, page_count, has_comicinfo, cover_path, parsed_series,
			parsed_number, parsed_year, match_confidence, created_at, updated_at
		FROM comic_files
		WHERE file_hash IS NOT NULL AND file_hash != ''
			AND file_hash IN (
				SELECT file_hash FROM comic_files
				WHERE file_hash IS NOT NULL AND file_hash != ''
				GROUP BY file_hash HAVING COUNT(*) > 1
			)
		ORDER BY file_hash, id`)
	if err != nil {
		return nil, fmt.Errorf("finding hash duplicates: %w", err)
	}
	defer rows.Close()

	groups := make(map[string]*DuplicateGroup)
	var order []string
	for rows.Next() {
		f, err := scanComicFileRow(rows)
		if err != nil {
			return nil, err
		}
		if _, ok := groups[f.FileHash]; !ok {
			groups[f.FileHash] = &DuplicateGroup{Key: "hash:" + f.FileHash}
			order = append(order, f.FileHash)
		}
		groups[f.FileHash].Files = append(groups[f.FileHash].Files, *f)
	}

	result := make([]DuplicateGroup, 0, len(order))
	for _, k := range order {
		result = append(result, *groups[k])
	}
	return result, nil
}

// FindDuplicatesByIssue returns groups of files linked to the same issue.
func (r *FileRepo) FindDuplicatesByIssue() ([]DuplicateGroup, error) {
	rows, err := r.read.Query(`
		SELECT cf.id, cf.issue_id, cf.file_path, cf.file_name, cf.file_size, cf.file_hash,
			cf.file_format, cf.page_count, cf.has_comicinfo, cf.cover_path, cf.parsed_series,
			cf.parsed_number, cf.parsed_year, cf.match_confidence, cf.created_at, cf.updated_at
		FROM comic_files cf
		WHERE cf.issue_id IS NOT NULL
			AND cf.issue_id IN (
				SELECT issue_id FROM comic_files
				WHERE issue_id IS NOT NULL
				GROUP BY issue_id HAVING COUNT(*) > 1
			)
		ORDER BY cf.issue_id, cf.id`)
	if err != nil {
		return nil, fmt.Errorf("finding issue duplicates: %w", err)
	}
	defer rows.Close()

	groups := make(map[int64]*DuplicateGroup)
	var order []int64
	for rows.Next() {
		f, err := scanComicFileRow(rows)
		if err != nil {
			return nil, err
		}
		issueID := *f.IssueID
		if _, ok := groups[issueID]; !ok {
			groups[issueID] = &DuplicateGroup{Key: fmt.Sprintf("issue:%d", issueID)}
			order = append(order, issueID)
		}
		groups[issueID].Files = append(groups[issueID].Files, *f)
	}

	result := make([]DuplicateGroup, 0, len(order))
	for _, k := range order {
		result = append(result, *groups[k])
	}
	return result, nil
}

// ListUnhashed returns files with no file_hash.
func (r *FileRepo) ListUnhashed() ([]model.ComicFile, error) {
	rows, err := r.read.Query(`
		SELECT id, issue_id, file_path, file_name, file_size, file_hash,
			file_format, page_count, has_comicinfo, cover_path, parsed_series,
			parsed_number, parsed_year, match_confidence, created_at, updated_at
		FROM comic_files
		WHERE file_hash IS NULL OR file_hash = ''
		ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listing unhashed files: %w", err)
	}
	defer rows.Close()

	var files []model.ComicFile
	for rows.Next() {
		f, err := scanComicFileRow(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, *f)
	}
	return files, nil
}

// scanner interface for QueryRow and Rows
type scannable interface {
	Scan(dest ...any) error
}

func scanComicFile(row *sql.Row) (*model.ComicFile, error) {
	f := &model.ComicFile{}
	var createdAt, updatedAt string
	err := row.Scan(
		&f.ID, &f.IssueID, &f.FilePath, &f.FileName, &f.FileSize, &f.FileHash,
		&f.FileFormat, &f.PageCount, &f.HasComicInfo, &f.CoverPath, &f.ParsedSeries,
		&f.ParsedNumber, &f.ParsedYear, &f.MatchConfidence, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning comic file: %w", err)
	}
	f.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	f.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return f, nil
}

func scanComicFileRow(rows *sql.Rows) (*model.ComicFile, error) {
	f := &model.ComicFile{}
	var createdAt, updatedAt string
	err := rows.Scan(
		&f.ID, &f.IssueID, &f.FilePath, &f.FileName, &f.FileSize, &f.FileHash,
		&f.FileFormat, &f.PageCount, &f.HasComicInfo, &f.CoverPath, &f.ParsedSeries,
		&f.ParsedNumber, &f.ParsedYear, &f.MatchConfidence, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scanning comic file row: %w", err)
	}
	f.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	f.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return f, nil
}

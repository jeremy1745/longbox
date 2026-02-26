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

func (r *FileRepo) Delete(id int64) error {
	_, err := r.write.Exec(`DELETE FROM comic_files WHERE id = ?`, id)
	return err
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

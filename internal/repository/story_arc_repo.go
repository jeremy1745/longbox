package repository

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/jeremy/longbox/internal/model"
)

type StoryArcRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewStoryArcRepo(read, write *sql.DB) *StoryArcRepo {
	return &StoryArcRepo{read: read, write: write}
}

// Create inserts a new story arc.
func (r *StoryArcRepo) Create(arc *model.StoryArc) error {
	res, err := r.write.Exec(`
		INSERT INTO story_arcs (name, comicvine_id, description)
		VALUES (?, ?, ?)`,
		arc.Name, arc.ComicVineID, arc.Description,
	)
	if err != nil {
		return fmt.Errorf("inserting story arc: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting last insert id: %w", err)
	}
	arc.ID = id
	return nil
}

// GetByID fetches a story arc with count subqueries.
func (r *StoryArcRepo) GetByID(id int64) (*model.StoryArc, error) {
	row := r.read.QueryRow(`
		SELECT sa.id, sa.name, sa.comicvine_id, COALESCE(sa.description,''),
			sa.created_at, sa.updated_at,
			COALESCE((SELECT COUNT(*) FROM story_arc_issues WHERE story_arc_id = sa.id), 0) as issue_count,
			COALESCE((SELECT COUNT(*) FROM story_arc_issues sai
				JOIN comic_files cf ON cf.issue_id = sai.issue_id
				WHERE sai.story_arc_id = sa.id), 0) as owned_count
		FROM story_arcs sa
		WHERE sa.id = ?`, id)

	arc := &model.StoryArc{}
	var createdAt, updatedAt string
	err := row.Scan(
		&arc.ID, &arc.Name, &arc.ComicVineID, &arc.Description,
		&createdAt, &updatedAt,
		&arc.IssueCount, &arc.OwnedCount,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning story arc: %w", err)
	}
	arc.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	arc.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return arc, nil
}

// GetByComicVineID finds a story arc by its ComicVine ID.
func (r *StoryArcRepo) GetByComicVineID(cvID int64) (*model.StoryArc, error) {
	row := r.read.QueryRow(`
		SELECT sa.id, sa.name, sa.comicvine_id, COALESCE(sa.description,''),
			sa.created_at, sa.updated_at,
			COALESCE((SELECT COUNT(*) FROM story_arc_issues WHERE story_arc_id = sa.id), 0) as issue_count,
			COALESCE((SELECT COUNT(*) FROM story_arc_issues sai
				JOIN comic_files cf ON cf.issue_id = sai.issue_id
				WHERE sai.story_arc_id = sa.id), 0) as owned_count
		FROM story_arcs sa
		WHERE sa.comicvine_id = ?`, cvID)

	arc := &model.StoryArc{}
	var createdAt, updatedAt string
	err := row.Scan(
		&arc.ID, &arc.Name, &arc.ComicVineID, &arc.Description,
		&createdAt, &updatedAt,
		&arc.IssueCount, &arc.OwnedCount,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning story arc: %w", err)
	}
	arc.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	arc.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
	return arc, nil
}

// List returns all story arcs with counts.
func (r *StoryArcRepo) List() ([]model.StoryArc, error) {
	rows, err := r.read.Query(`
		SELECT sa.id, sa.name, sa.comicvine_id, COALESCE(sa.description,''),
			sa.created_at, sa.updated_at,
			COALESCE((SELECT COUNT(*) FROM story_arc_issues WHERE story_arc_id = sa.id), 0) as issue_count,
			COALESCE((SELECT COUNT(*) FROM story_arc_issues sai
				JOIN comic_files cf ON cf.issue_id = sai.issue_id
				WHERE sai.story_arc_id = sa.id), 0) as owned_count
		FROM story_arcs sa
		ORDER BY sa.name ASC`)
	if err != nil {
		return nil, fmt.Errorf("listing story arcs: %w", err)
	}
	defer rows.Close()

	var arcs []model.StoryArc
	for rows.Next() {
		arc := model.StoryArc{}
		var createdAt, updatedAt string
		if err := rows.Scan(
			&arc.ID, &arc.Name, &arc.ComicVineID, &arc.Description,
			&createdAt, &updatedAt,
			&arc.IssueCount, &arc.OwnedCount,
		); err != nil {
			return nil, fmt.Errorf("scanning story arc row: %w", err)
		}
		arc.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		arc.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		arcs = append(arcs, arc)
	}
	return arcs, nil
}

// Delete removes a story arc and its issue links (CASCADE).
func (r *StoryArcRepo) Delete(id int64) error {
	_, err := r.write.Exec(`DELETE FROM story_arcs WHERE id = ?`, id)
	return err
}

// AddIssue links an issue to a story arc.
func (r *StoryArcRepo) AddIssue(arcID, issueID int64, sequenceNumber *int) error {
	_, err := r.write.Exec(`
		INSERT OR IGNORE INTO story_arc_issues (story_arc_id, issue_id, sequence_number)
		VALUES (?, ?, ?)`, arcID, issueID, sequenceNumber)
	return err
}

// RemoveIssue unlinks an issue from a story arc.
func (r *StoryArcRepo) RemoveIssue(arcID, issueID int64) error {
	_, err := r.write.Exec(`DELETE FROM story_arc_issues WHERE story_arc_id = ? AND issue_id = ?`,
		arcID, issueID)
	return err
}

// ListIssues returns all issues in a story arc with joined data.
func (r *StoryArcRepo) ListIssues(arcID int64) ([]model.StoryArcIssue, error) {
	rows, err := r.read.Query(`
		SELECT sai.story_arc_id, sai.issue_id, sai.sequence_number,
			COALESCE(s.title, ''), COALESCE(i.issue_number, ''),
			COALESCE(i.cover_url, ''),
			CASE WHEN cf.id IS NOT NULL THEN 1 ELSE 0 END as has_file,
			COALESCE(i.read_status, 'unread')
		FROM story_arc_issues sai
		JOIN issues i ON sai.issue_id = i.id
		JOIN series s ON i.series_id = s.id
		LEFT JOIN comic_files cf ON cf.issue_id = i.id
		WHERE sai.story_arc_id = ?
		ORDER BY COALESCE(sai.sequence_number, 999999), i.store_date, s.title, i.sort_number`, arcID)
	if err != nil {
		return nil, fmt.Errorf("listing arc issues: %w", err)
	}
	defer rows.Close()

	var issues []model.StoryArcIssue
	for rows.Next() {
		var sai model.StoryArcIssue
		if err := rows.Scan(
			&sai.StoryArcID, &sai.IssueID, &sai.SequenceNumber,
			&sai.SeriesTitle, &sai.IssueNumber,
			&sai.CoverURL, &sai.HasFile, &sai.ReadStatus,
		); err != nil {
			return nil, fmt.Errorf("scanning arc issue: %w", err)
		}
		issues = append(issues, sai)
	}
	return issues, nil
}

package model

import "time"

type ComicFile struct {
	ID              int64      `json:"id"`
	IssueID         *int64     `json:"issue_id,omitempty"`
	FilePath        string     `json:"file_path"`
	FileName        string     `json:"file_name"`
	FileSize        int64      `json:"file_size"`
	FileHash        string     `json:"file_hash,omitempty"`
	FileFormat      string     `json:"file_format"`
	PageCount       *int       `json:"page_count,omitempty"`
	HasComicInfo    bool       `json:"has_comicinfo"`
	CoverPath       string     `json:"cover_path,omitempty"`
	ParsedSeries    string     `json:"parsed_series,omitempty"`
	ParsedNumber    string     `json:"parsed_number,omitempty"`
	ParsedYear      *int       `json:"parsed_year,omitempty"`
	MatchConfidence float64    `json:"match_confidence"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

package repository

import (
	"database/sql"
	"fmt"

	"github.com/jeremy/longbox/internal/model"
)

type BlocklistRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewBlocklistRepo(read, write *sql.DB) *BlocklistRepo {
	return &BlocklistRepo{read: read, write: write}
}

// Add inserts a new blocklist entry (ignores duplicates by GUID).
func (r *BlocklistRepo) Add(guid, name, reason string) error {
	_, err := r.write.Exec(`INSERT OR IGNORE INTO search_blocklist (nzb_guid, nzb_name, reason) VALUES (?, ?, ?)`,
		guid, name, reason)
	return err
}

// IsBlocked checks if a GUID is on the blocklist.
func (r *BlocklistRepo) IsBlocked(guid string) (bool, error) {
	var count int
	err := r.read.QueryRow(`SELECT COUNT(*) FROM search_blocklist WHERE nzb_guid = ?`, guid).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("checking blocklist: %w", err)
	}
	return count > 0, nil
}

// List returns paginated blocklist entries.
func (r *BlocklistRepo) List(page, perPage int) ([]model.BlocklistEntry, int, error) {
	if page < 1 {
		page = 1
	}
	if perPage < 1 {
		perPage = 50
	}
	offset := (page - 1) * perPage

	var total int
	if err := r.read.QueryRow(`SELECT COUNT(*) FROM search_blocklist`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting blocklist: %w", err)
	}

	rows, err := r.read.Query(`
		SELECT id, nzb_guid, nzb_name, reason, blocked_at
		FROM search_blocklist
		ORDER BY id DESC
		LIMIT ? OFFSET ?`, perPage, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("listing blocklist: %w", err)
	}
	defer rows.Close()

	var entries []model.BlocklistEntry
	for rows.Next() {
		var e model.BlocklistEntry
		if err := rows.Scan(&e.ID, &e.NZBGuid, &e.NZBName, &e.Reason, &e.BlockedAt); err != nil {
			return nil, 0, fmt.Errorf("scanning blocklist entry: %w", err)
		}
		entries = append(entries, e)
	}
	return entries, total, nil
}

// Delete removes a single blocklist entry.
func (r *BlocklistRepo) Delete(id int64) error {
	_, err := r.write.Exec(`DELETE FROM search_blocklist WHERE id = ?`, id)
	return err
}

// Clear removes all blocklist entries.
func (r *BlocklistRepo) Clear() error {
	_, err := r.write.Exec(`DELETE FROM search_blocklist`)
	return err
}

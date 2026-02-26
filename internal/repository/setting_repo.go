package repository

import (
	"database/sql"
	"fmt"
	"time"
)

type SettingRepo struct {
	read  *sql.DB
	write *sql.DB
}

func NewSettingRepo(read, write *sql.DB) *SettingRepo {
	return &SettingRepo{read: read, write: write}
}

// Get retrieves a setting value by key. Returns empty string if not found.
func (r *SettingRepo) Get(key string) (string, error) {
	var value string
	err := r.read.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading setting %q: %w", key, err)
	}
	return value, nil
}

// Set creates or updates a setting value.
func (r *SettingRepo) Set(key, value string) error {
	_, err := r.write.Exec(`
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("setting %q: %w", key, err)
	}
	return nil
}

// Delete removes a setting by key.
func (r *SettingRepo) Delete(key string) error {
	_, err := r.write.Exec(`DELETE FROM settings WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("deleting setting %q: %w", key, err)
	}
	return nil
}

// GetAll returns all settings as a key-value map.
func (r *SettingRepo) GetAll() (map[string]string, error) {
	rows, err := r.read.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("listing settings: %w", err)
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scanning setting: %w", err)
		}
		settings[key] = value
	}
	return settings, nil
}

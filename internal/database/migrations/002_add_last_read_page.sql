-- +goose Up
ALTER TABLE issues ADD COLUMN last_read_page INTEGER;

-- +goose Down
-- SQLite does not support DROP COLUMN in older versions; no-op for safety.

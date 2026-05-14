-- +goose Up
ALTER TABLE want_list ADD COLUMN last_searched_at TEXT;

-- +goose Down
ALTER TABLE want_list DROP COLUMN last_searched_at;

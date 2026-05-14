-- +goose Up
ALTER TABLE want_list ADD COLUMN procurement_status TEXT NOT NULL DEFAULT 'none' CHECK (procurement_status IN ('none','pending','submitted','acquired','failed'));
ALTER TABLE want_list ADD COLUMN procurement_submitted_at TEXT;
ALTER TABLE want_list ADD COLUMN procurement_last_error TEXT;

-- +goose Down
-- No restore: ADD COLUMN has no clean reverse in SQLite pre-3.35. The columns
-- are additive and carry no data that cannot be recreated.
SELECT 1;

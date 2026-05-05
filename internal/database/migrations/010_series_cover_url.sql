-- +goose Up

-- External cover image URL sourced from the metadata provider (ComicVine
-- volume image, Metron series cover, etc.). Used as a fallback in the UI
-- when no local file thumbnail exists yet — important for series that are
-- matched but not-yet-owned.
ALTER TABLE series ADD COLUMN cover_image_url TEXT;

-- +goose Down
-- SQLite cannot drop columns; the column remains unused on downgrade.

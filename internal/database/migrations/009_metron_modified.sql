-- +goose Up

-- Records the Last-Modified header value from the most recent Metron detail
-- fetch. Used as the If-Modified-Since on subsequent requests so unchanged
-- resources return 304 — and 304s do not count against the Metron quota.
ALTER TABLE series ADD COLUMN metron_modified_at TEXT;
ALTER TABLE issues ADD COLUMN metron_modified_at TEXT;

-- +goose Down
-- SQLite cannot drop columns; the column remains but is unused on downgrade.

-- +goose Up

-- Feature 2: Per-Issue Skip Status
ALTER TABLE issues ADD COLUMN skip_status TEXT;

-- Feature 4: Annuals parent linking
ALTER TABLE series ADD COLUMN parent_series_id INTEGER REFERENCES series(id) ON DELETE SET NULL;
CREATE INDEX idx_series_parent ON series(parent_series_id);

-- Feature 5: Duplicate Detection (file_hash column already exists, add index)
CREATE INDEX idx_comic_files_hash ON comic_files(file_hash) WHERE file_hash IS NOT NULL AND file_hash != '';

-- Feature 7: Blocklist
CREATE TABLE search_blocklist (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    nzb_guid    TEXT NOT NULL,
    nzb_name    TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    blocked_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
CREATE UNIQUE INDEX idx_blocklist_guid ON search_blocklist(nzb_guid);

-- Feature 7: Store GUID in download history for blocklisting on failure
ALTER TABLE download_history ADD COLUMN nzb_guid TEXT;

-- +goose Down
ALTER TABLE download_history DROP COLUMN nzb_guid;
DROP INDEX IF EXISTS idx_blocklist_guid;
DROP TABLE IF EXISTS search_blocklist;
DROP INDEX IF EXISTS idx_series_parent;
ALTER TABLE series DROP COLUMN parent_series_id;
DROP INDEX IF EXISTS idx_comic_files_hash;
ALTER TABLE issues DROP COLUMN skip_status;

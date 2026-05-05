-- +goose Up

ALTER TABLE series ADD COLUMN metron_id INTEGER;
CREATE UNIQUE INDEX idx_series_metron_id ON series(metron_id) WHERE metron_id IS NOT NULL;

ALTER TABLE issues ADD COLUMN metron_id INTEGER;
CREATE UNIQUE INDEX idx_issues_metron_id ON issues(metron_id) WHERE metron_id IS NOT NULL;

-- +goose Down

DROP INDEX IF EXISTS idx_issues_metron_id;
DROP INDEX IF EXISTS idx_series_metron_id;

-- SQLite cannot drop columns from older versions; the columns remain on
-- downgrade but stop being indexed. New uniqueness constraints can be
-- recreated via the Up step.

-- +goose Up

-- 579 series rows on the live DB have cover_file_id pointing to a
-- comic_files row that no longer exists (dedupe/cleanup operations
-- removed the file rows but left the series pointer behind). The cover
-- handler 404s those, so the library dashboard shows a "?" placeholder
-- on every affected card.
--
-- Two-step repair:
--   1. NULL out any cover_file_id that no longer resolves.
--   2. For series with NULL cover_file_id AND at least one linked
--      comic_file, set cover_file_id to the file under the earliest
--      issue (by sort_number, then by file id). The cover handler
--      extracts the cover on first hit, so no extra work needed here.
--
-- Series with no linked files stay NULL — their cards still render with
-- a placeholder, but at least the card has accurate intent (no files yet).

-- 1. Clear stale pointers
UPDATE series
SET cover_file_id = NULL
WHERE cover_file_id IS NOT NULL
  AND cover_file_id NOT IN (SELECT id FROM comic_files);

-- 2. Repoint to the first available file per series.
--    A CTE picks the earliest comic_file id under the lowest-numbered
--    issue. Series with zero linked files get nothing.
WITH first_file AS (
    SELECT s.id AS series_id,
           (SELECT cf.id
              FROM comic_files cf
              JOIN issues i ON cf.issue_id = i.id
             WHERE i.series_id = s.id
             ORDER BY i.sort_number ASC, cf.id ASC
             LIMIT 1) AS file_id
      FROM series s
     WHERE s.cover_file_id IS NULL
)
UPDATE series
   SET cover_file_id = (SELECT file_id FROM first_file WHERE series_id = series.id)
 WHERE cover_file_id IS NULL
   AND id IN (SELECT series_id FROM first_file WHERE file_id IS NOT NULL);

-- 3. Forward-fix: trigger NULLs any series.cover_file_id when its
--    referenced comic_file is deleted, so this can't reproduce.
CREATE TRIGGER IF NOT EXISTS comic_files_clear_series_cover
AFTER DELETE ON comic_files
BEGIN
    UPDATE series SET cover_file_id = NULL WHERE cover_file_id = OLD.id;
END;

-- +goose Down
DROP TRIGGER IF EXISTS comic_files_clear_series_cover;
SELECT 1;

-- +goose Up

-- One-time dedupe of series and issues, then add UNIQUE constraints so
-- duplicates can't reproduce.
--
-- The 173-row series table on the live server has 14 file-shaped rows
-- (e.g. "20th Century Men 01 (of 06) (2022)") plus several pairs that
-- differ only by punctuation ("30 Days of Night Falling Sun" vs
-- "30 Days of Night: Falling Sun"). The issues table has matching dupes.
--
-- For each group with the same normalized key we pick a canonical row,
-- re-point every foreign key that referenced a non-canonical row, then
-- delete the losers.
--
-- Canonical choice for series (best first):
--   1. tracked=1 AND comicvine_id IS NOT NULL
--   2. tracked=1
--   3. comicvine_id IS NOT NULL
--   4. lowest id (deterministic tiebreak)
--
-- Canonical choice for issues:
--   1. comicvine_id IS NOT NULL
--   2. lowest id

-- ---- SERIES DEDUPE ----

CREATE TEMPORARY TABLE _series_dedupe AS
SELECT
    s.id,
    FIRST_VALUE(s.id) OVER w AS canonical_id
FROM series s
WINDOW w AS (
    PARTITION BY LOWER(TRIM(s.title)), COALESCE(s.year, -1)
    ORDER BY
        (CASE
            WHEN s.tracked = 1 AND s.comicvine_id IS NOT NULL THEN 0
            WHEN s.tracked = 1                                THEN 1
            WHEN s.comicvine_id IS NOT NULL                   THEN 2
            ELSE 3
         END),
        s.id
);

-- Repoint children before deleting the loser series rows.
UPDATE issues
SET series_id = (SELECT canonical_id FROM _series_dedupe WHERE _series_dedupe.id = issues.series_id)
WHERE series_id IN (SELECT id FROM _series_dedupe WHERE id != canonical_id);

UPDATE backlog_runs
SET series_id = (SELECT canonical_id FROM _series_dedupe WHERE _series_dedupe.id = backlog_runs.series_id)
WHERE series_id IN (SELECT id FROM _series_dedupe WHERE id != canonical_id);

UPDATE backlog_items
SET series_id = (SELECT canonical_id FROM _series_dedupe WHERE _series_dedupe.id = backlog_items.series_id)
WHERE series_id IN (SELECT id FROM _series_dedupe WHERE id != canonical_id);

-- Annual parent linkage (006_features.sql).
UPDATE series
SET parent_series_id = (
    SELECT canonical_id FROM _series_dedupe WHERE _series_dedupe.id = series.parent_series_id
)
WHERE parent_series_id IS NOT NULL
  AND parent_series_id IN (SELECT id FROM _series_dedupe WHERE id != canonical_id);

DELETE FROM series WHERE id IN (SELECT id FROM _series_dedupe WHERE id != canonical_id);

DROP TABLE _series_dedupe;

-- ---- ISSUES DEDUPE ----

CREATE TEMPORARY TABLE _issues_dedupe AS
SELECT
    i.id,
    FIRST_VALUE(i.id) OVER w AS canonical_id
FROM issues i
WINDOW w AS (
    PARTITION BY i.series_id, LOWER(TRIM(i.issue_number))
    ORDER BY
        (CASE WHEN i.comicvine_id IS NOT NULL THEN 0 ELSE 1 END),
        i.id
);

-- want_list has UNIQUE(issue_id), so first drop dupe-pointing rows where
-- the canonical issue already has a want_list entry, then repoint the rest.
DELETE FROM want_list
WHERE issue_id IN (SELECT id FROM _issues_dedupe WHERE id != canonical_id)
  AND EXISTS (
    SELECT 1 FROM want_list w2, _issues_dedupe d
    WHERE d.id = want_list.issue_id AND w2.issue_id = d.canonical_id
);

UPDATE want_list
SET issue_id = (SELECT canonical_id FROM _issues_dedupe WHERE _issues_dedupe.id = want_list.issue_id)
WHERE issue_id IN (SELECT id FROM _issues_dedupe WHERE id != canonical_id);

-- story_arc_issues has PRIMARY KEY(story_arc_id, issue_id) — same pattern.
DELETE FROM story_arc_issues
WHERE issue_id IN (SELECT id FROM _issues_dedupe WHERE id != canonical_id)
  AND EXISTS (
    SELECT 1
    FROM story_arc_issues sai2, _issues_dedupe d
    WHERE d.id = story_arc_issues.issue_id
      AND sai2.story_arc_id = story_arc_issues.story_arc_id
      AND sai2.issue_id = d.canonical_id
);

UPDATE story_arc_issues
SET issue_id = (SELECT canonical_id FROM _issues_dedupe WHERE _issues_dedupe.id = story_arc_issues.issue_id)
WHERE issue_id IN (SELECT id FROM _issues_dedupe WHERE id != canonical_id);

-- comic_files / download_history / backlog_items: no UNIQUE on issue_id,
-- straight repoint.
UPDATE comic_files
SET issue_id = (SELECT canonical_id FROM _issues_dedupe WHERE _issues_dedupe.id = comic_files.issue_id)
WHERE issue_id IS NOT NULL
  AND issue_id IN (SELECT id FROM _issues_dedupe WHERE id != canonical_id);

UPDATE download_history
SET issue_id = (SELECT canonical_id FROM _issues_dedupe WHERE _issues_dedupe.id = download_history.issue_id)
WHERE issue_id IS NOT NULL
  AND issue_id IN (SELECT id FROM _issues_dedupe WHERE id != canonical_id);

UPDATE backlog_items
SET issue_id = (SELECT canonical_id FROM _issues_dedupe WHERE _issues_dedupe.id = backlog_items.issue_id)
WHERE issue_id IS NOT NULL
  AND issue_id IN (SELECT id FROM _issues_dedupe WHERE id != canonical_id);

DELETE FROM issues WHERE id IN (SELECT id FROM _issues_dedupe WHERE id != canonical_id);

DROP TABLE _issues_dedupe;

-- ---- UNIQUE CONSTRAINTS ----

CREATE UNIQUE INDEX ux_series_norm_title_year
    ON series (LOWER(TRIM(title)), COALESCE(year, -1));

CREATE UNIQUE INDEX ux_issues_series_norm_number
    ON issues (series_id, LOWER(TRIM(issue_number)));

-- +goose Down
DROP INDEX IF EXISTS ux_issues_series_norm_number;
DROP INDEX IF EXISTS ux_series_norm_title_year;

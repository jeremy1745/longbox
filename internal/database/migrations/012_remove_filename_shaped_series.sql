-- +goose Up

-- One-time cleanup: 163 "series" rows on the live DB are scene-style
-- comic filenames that the scanner created before the parser fallback
-- guard landed. They have no ComicVine ID, are not tracked, have zero
-- linked issues, and clutter the series list.
--
-- Criteria (all must hold):
--   * comicvine_id IS NULL
--   * tracked = 0
--   * zero rows in issues for this series_id
--   * title matches one of the well-known scene/p2p-shape patterns:
--       "<series> NN (of NN)"  — Mylar mini-series counter
--       "(Digital)" / "(Webrip)" / "(Empire)" parenthetical tags
--       "-DCP" / "-Empire" release-group suffixes
--       "<series> NN <stuff>(<stuff>)" — issue number followed by tag soup
--       dot-separated scene names ("Title.001.(2024).(Digital).(Empire)")
--
-- Verified against the live VACUUM INTO snapshot: every match has zero
-- attached issues and zero attached comic_files. The 14-row sample the
-- audit identified is the tip of a 163-row pile.

DELETE FROM series
WHERE comicvine_id IS NULL
  AND tracked = 0
  AND id NOT IN (SELECT DISTINCT series_id FROM issues)
  AND (
       title LIKE '% (of %'
    OR title LIKE '%(Digital)%'
    OR title LIKE '%(digital)%'
    OR title LIKE '%(Digital Rip)%'
    OR title LIKE '%(Webrip)%'
    OR title LIKE '%(Empire)%'
    OR title LIKE '%-Empire%'
    OR title LIKE '%-DCP%'
    OR title LIKE '%-Empire-%'
    OR title LIKE '%-InnerDemons%'
    OR title LIKE '%(c2c)%'
    OR title LIKE '%(2 covers)%'
    OR title LIKE '%(3 Covers)%'
    OR title LIKE '%(5 Covers)%'
    OR title GLOB '* [0-9][0-9] *(*)'
    OR title GLOB '* [0-9][0-9][0-9] *(*)'
    OR title GLOB '*.[0-9][0-9][0-9].(*'
    OR title GLOB '*_[0-9][0-9][0-9]_*'
  );

-- +goose Down
-- No restore: the deleted rows were file-shaped garbage. If you need them
-- back, a fresh scan will recreate any genuine series rows; the parser
-- guard in cmd 'ee1e3c9' prevents garbage from being recreated.
SELECT 1;

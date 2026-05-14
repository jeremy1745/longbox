-- +goose Up

-- ComicInfo.xml files in the wild use inconsistent publisher names. The
-- publisher-backfill pass (introduced in d91b067) read every <Publisher>
-- tag verbatim, leaving the publishers table with rows like:
--
--   DC                  (50 series)   AND  DC Comics            (133 series)
--   Marvel              (427)         AND  Marvel Comics        (1)
--   Image               (251)         AND  Image Comics         (3)
--   Dark Horse          (32)          AND  Dark Horse Comics    (48)
--   IDW                 (8)           AND  IDW Publishing       (13)
--   Titan               (3)           AND  Titan Comics         (7)
--
-- Canonical chosen as the longer / industry-standard spelling. For each
-- alias: ensure the canonical row exists, repoint series.publisher_id from
-- alias → canonical, then delete the alias row.
--
-- Imprints like "Image - Skybound" / "Image - Top Cow" are intentionally
-- left distinct — they're meaningful at the catalog level.

INSERT OR IGNORE INTO publishers (name) VALUES ('DC Comics');
UPDATE series SET publisher_id = (SELECT id FROM publishers WHERE LOWER(name)='dc comics')
    WHERE publisher_id IN (SELECT id FROM publishers WHERE LOWER(name)='dc');
DELETE FROM publishers WHERE LOWER(name) = 'dc';

INSERT OR IGNORE INTO publishers (name) VALUES ('Marvel Comics');
UPDATE series SET publisher_id = (SELECT id FROM publishers WHERE LOWER(name)='marvel comics')
    WHERE publisher_id IN (SELECT id FROM publishers WHERE LOWER(name)='marvel');
DELETE FROM publishers WHERE LOWER(name) = 'marvel';

INSERT OR IGNORE INTO publishers (name) VALUES ('Image Comics');
UPDATE series SET publisher_id = (SELECT id FROM publishers WHERE LOWER(name)='image comics')
    WHERE publisher_id IN (SELECT id FROM publishers WHERE LOWER(name)='image');
DELETE FROM publishers WHERE LOWER(name) = 'image';

INSERT OR IGNORE INTO publishers (name) VALUES ('Dark Horse Comics');
UPDATE series SET publisher_id = (SELECT id FROM publishers WHERE LOWER(name)='dark horse comics')
    WHERE publisher_id IN (SELECT id FROM publishers WHERE LOWER(name)='dark horse');
DELETE FROM publishers WHERE LOWER(name) = 'dark horse';

INSERT OR IGNORE INTO publishers (name) VALUES ('IDW Publishing');
UPDATE series SET publisher_id = (SELECT id FROM publishers WHERE LOWER(name)='idw publishing')
    WHERE publisher_id IN (SELECT id FROM publishers WHERE LOWER(name)='idw');
DELETE FROM publishers WHERE LOWER(name) = 'idw';

INSERT OR IGNORE INTO publishers (name) VALUES ('Titan Comics');
UPDATE series SET publisher_id = (SELECT id FROM publishers WHERE LOWER(name)='titan comics')
    WHERE publisher_id IN (SELECT id FROM publishers WHERE LOWER(name)='titan');
DELETE FROM publishers WHERE LOWER(name) = 'titan';

-- +goose Down
-- No restore: alias rows held no data beyond their abbreviated name.
SELECT 1;

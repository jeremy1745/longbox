-- +goose Up

CREATE TABLE publishers (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL UNIQUE,
    comicvine_id    INTEGER UNIQUE,
    logo_url        TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_publishers_comicvine_id ON publishers(comicvine_id);

CREATE TABLE series (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    title           TEXT NOT NULL,
    sort_title      TEXT NOT NULL,
    year            INTEGER,
    publisher_id    INTEGER REFERENCES publishers(id) ON DELETE SET NULL,
    comicvine_id    INTEGER UNIQUE,
    description     TEXT,
    status          TEXT NOT NULL DEFAULT 'continuing',
    total_issues    INTEGER DEFAULT 0,
    cover_file_id   INTEGER,
    tracked         INTEGER NOT NULL DEFAULT 0,
    metadata_locked INTEGER NOT NULL DEFAULT 0,
    last_cv_sync    TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_series_title ON series(sort_title);
CREATE INDEX idx_series_publisher ON series(publisher_id);
CREATE INDEX idx_series_comicvine ON series(comicvine_id);
CREATE INDEX idx_series_tracked ON series(tracked);

CREATE TABLE issues (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id       INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    issue_number    TEXT NOT NULL,
    sort_number     REAL NOT NULL DEFAULT 0,
    title           TEXT,
    comicvine_id    INTEGER UNIQUE,
    description     TEXT,
    cover_date      TEXT,
    store_date      TEXT,
    cover_url       TEXT,
    writers         TEXT,
    artists         TEXT,
    read_status     TEXT NOT NULL DEFAULT 'unread',
    rating          INTEGER,
    metadata_locked INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_issues_series ON issues(series_id, sort_number);
CREATE INDEX idx_issues_comicvine ON issues(comicvine_id);
CREATE INDEX idx_issues_store_date ON issues(store_date);
CREATE INDEX idx_issues_read_status ON issues(read_status);

CREATE TABLE comic_files (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_id        INTEGER REFERENCES issues(id) ON DELETE SET NULL,
    file_path       TEXT NOT NULL UNIQUE,
    file_name       TEXT NOT NULL,
    file_size       INTEGER NOT NULL,
    file_hash       TEXT,
    file_format     TEXT NOT NULL,
    page_count      INTEGER,
    has_comicinfo   INTEGER NOT NULL DEFAULT 0,
    cover_path      TEXT,
    parsed_series   TEXT,
    parsed_number   TEXT,
    parsed_year     INTEGER,
    match_confidence REAL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_comic_files_issue ON comic_files(issue_id);
CREATE INDEX idx_comic_files_path ON comic_files(file_path);
CREATE INDEX idx_comic_files_format ON comic_files(file_format);

CREATE TABLE story_arcs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    comicvine_id    INTEGER UNIQUE,
    description     TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE TABLE story_arc_issues (
    story_arc_id    INTEGER NOT NULL REFERENCES story_arcs(id) ON DELETE CASCADE,
    issue_id        INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    sequence_number INTEGER,
    PRIMARY KEY (story_arc_id, issue_id)
);

CREATE TABLE want_list (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_id        INTEGER NOT NULL UNIQUE REFERENCES issues(id) ON DELETE CASCADE,
    priority        INTEGER NOT NULL DEFAULT 0,
    notes           TEXT,
    added_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_want_list_priority ON want_list(priority DESC);

CREATE TABLE jobs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    type            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    progress        INTEGER DEFAULT 0,
    total_items     INTEGER DEFAULT 0,
    processed_items INTEGER DEFAULT 0,
    message         TEXT,
    started_at      TEXT,
    completed_at    TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_jobs_status ON jobs(status);
CREATE INDEX idx_jobs_type ON jobs(type);

CREATE TABLE settings (
    key             TEXT PRIMARY KEY,
    value           TEXT NOT NULL,
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE TABLE notifications (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    type            TEXT NOT NULL,
    title           TEXT NOT NULL,
    message         TEXT NOT NULL,
    read            INTEGER NOT NULL DEFAULT 0,
    related_type    TEXT,
    related_id      INTEGER,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_notifications_read ON notifications(read);

-- +goose Down
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS want_list;
DROP TABLE IF EXISTS story_arc_issues;
DROP TABLE IF EXISTS story_arcs;
DROP TABLE IF EXISTS comic_files;
DROP TABLE IF EXISTS issues;
DROP TABLE IF EXISTS series;
DROP TABLE IF EXISTS publishers;

-- +goose Up

CREATE TABLE backlog_runs (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id           INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    status              TEXT NOT NULL DEFAULT 'planning',
    include_variants    INTEGER NOT NULL DEFAULT 0,
    total_issues        INTEGER NOT NULL DEFAULT 0,
    queued_issues       INTEGER NOT NULL DEFAULT 0,
    completed_issues    INTEGER NOT NULL DEFAULT 0,
    failed_issues       INTEGER NOT NULL DEFAULT 0,
    paused              INTEGER NOT NULL DEFAULT 0,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_backlog_runs_series ON backlog_runs(series_id);
CREATE INDEX idx_backlog_runs_status ON backlog_runs(status);

CREATE TABLE backlog_items (
    id                   INTEGER PRIMARY KEY AUTOINCREMENT,
    backlog_run_id       INTEGER NOT NULL REFERENCES backlog_runs(id) ON DELETE CASCADE,
    series_id            INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    issue_id             INTEGER REFERENCES issues(id) ON DELETE SET NULL,
    variant_name         TEXT,
    priority             INTEGER NOT NULL DEFAULT 0,
    status               TEXT NOT NULL DEFAULT 'pending',
    retry_count          INTEGER NOT NULL DEFAULT 0,
    last_error           TEXT,
    retry_at             TEXT,
    sab_nzo_id           TEXT,
    nzb_guid             TEXT,
    download_history_id  INTEGER REFERENCES download_history(id) ON DELETE SET NULL,
    download_client_id   INTEGER REFERENCES download_clients(id) ON DELETE SET NULL,
    indexer_id           INTEGER REFERENCES indexers(id) ON DELETE SET NULL,
    created_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at           TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_backlog_items_run ON backlog_items(backlog_run_id);
CREATE INDEX idx_backlog_items_status ON backlog_items(status);
CREATE INDEX idx_backlog_items_retry_at ON backlog_items(retry_at);
CREATE INDEX idx_backlog_items_issue ON backlog_items(issue_id);
CREATE INDEX idx_backlog_items_download_history ON backlog_items(download_history_id);

-- +goose Down
DROP TABLE IF EXISTS backlog_items;
DROP TABLE IF EXISTS backlog_runs;

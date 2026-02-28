-- +goose Up

CREATE TABLE indexers (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    url             TEXT NOT NULL,
    api_key         TEXT NOT NULL,
    type            TEXT NOT NULL DEFAULT 'newznab',
    priority        INTEGER NOT NULL DEFAULT 50,
    enabled         INTEGER NOT NULL DEFAULT 1,
    categories      TEXT NOT NULL DEFAULT '7030',
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_indexers_enabled ON indexers(enabled);
CREATE INDEX idx_indexers_priority ON indexers(priority);

CREATE TABLE download_clients (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    type            TEXT NOT NULL DEFAULT 'sabnzbd',
    url             TEXT NOT NULL,
    api_key         TEXT NOT NULL,
    category        TEXT NOT NULL DEFAULT 'comics',
    priority        INTEGER NOT NULL DEFAULT 50,
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_download_clients_enabled ON download_clients(enabled);

CREATE TABLE download_history (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_id            INTEGER REFERENCES issues(id) ON DELETE SET NULL,
    indexer_id          INTEGER REFERENCES indexers(id) ON DELETE SET NULL,
    download_client_id  INTEGER REFERENCES download_clients(id) ON DELETE SET NULL,
    nzb_name            TEXT NOT NULL,
    nzb_url             TEXT,
    external_id         TEXT,
    status              TEXT NOT NULL DEFAULT 'grabbed',
    size                INTEGER DEFAULT 0,
    message             TEXT,
    grabbed_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    completed_at        TEXT,
    created_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

CREATE INDEX idx_download_history_issue ON download_history(issue_id);
CREATE INDEX idx_download_history_status ON download_history(status);
CREATE INDEX idx_download_history_external ON download_history(external_id);

-- +goose Down
DROP TABLE IF EXISTS download_history;
DROP TABLE IF EXISTS download_clients;
DROP TABLE IF EXISTS indexers;

package repository

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// setupWantListTestDB stands up an in-memory SQLite with the minimum schema
// needed to exercise WantListRepo. Same `db` handle is used as both read and
// write so a single connection is reused — which is exactly the condition
// under which sqlite3_last_insert_rowid leaks between calls.
func setupWantListTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	schema := `
		CREATE TABLE series (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			sort_title TEXT
		);
		CREATE TABLE issues (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			series_id INTEGER NOT NULL REFERENCES series(id),
			issue_number TEXT NOT NULL,
			sort_number REAL,
			cover_url TEXT,
			store_date TEXT,
			cover_date TEXT
		);
		CREATE TABLE want_list (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			issue_id INTEGER NOT NULL UNIQUE REFERENCES issues(id) ON DELETE CASCADE,
			priority INTEGER NOT NULL DEFAULT 0,
			notes TEXT,
			added_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
			last_searched_at TEXT
		);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO series (id, title) VALUES (1, 'Test')`); err != nil {
		t.Fatalf("seed series: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO issues (id, series_id, issue_number) VALUES (101, 1, '1'), (102, 1, '2')`); err != nil {
		t.Fatalf("seed issues: %v", err)
	}
	return db
}

// TestCreate_FirstInsertReturnsItem covers the simple happy path: a Create
// call for an issue that has no existing want_list row returns the new row.
func TestCreate_FirstInsertReturnsItem(t *testing.T) {
	db := setupWantListTestDB(t)
	defer db.Close()
	repo := NewWantListRepo(db, db)

	item, err := repo.Create(101, 0, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item == nil {
		t.Fatal("Create returned nil item on initial insert")
	}
	if item.IssueID != 101 {
		t.Errorf("expected IssueID=101, got %d", item.IssueID)
	}
}

// TestCreate_DuplicateAfterPoisonedRowid covers the production crash path.
// Calling Create for an already-wanted issue uses INSERT OR IGNORE, which
// silently no-ops. The bug: the previous code branched on
// `Result.LastInsertId() == 0`, but sqlite3_last_insert_rowid is NOT reset
// by an ignored insert — it keeps the rowid from the prior successful
// insert on the connection (e.g., an issues row created moments earlier by
// the metadata service). GetByID(thatPoisonedRowid) then queried
// want_list.id and missed, returning (nil, nil), and the calendar handler
// dereferenced item.IssueID → panic → recovery middleware → HTTP 500.
//
// This test primes sqlite3_last_insert_rowid with an unrelated issues
// insert, then re-Creates a want for the already-wanted issue and asserts
// the existing row comes back, not nil.
func TestCreate_DuplicateAfterPoisonedRowid(t *testing.T) {
	db := setupWantListTestDB(t)
	defer db.Close()
	repo := NewWantListRepo(db, db)

	if _, err := repo.Create(101, 0, ""); err != nil {
		t.Fatalf("initial want: %v", err)
	}

	// Prime sqlite3_last_insert_rowid with an issues insert on the same
	// connection, just like MetadataService does immediately before
	// calling WantListRepo.Create in WantIssueBySeriesAndNumber.
	if _, err := db.Exec(`INSERT INTO issues (id, series_id, issue_number) VALUES (999, 1, '99')`); err != nil {
		t.Fatalf("priming insert: %v", err)
	}

	item, err := repo.Create(101, 0, "")
	if err != nil {
		t.Fatalf("duplicate Create returned err: %v", err)
	}
	if item == nil {
		t.Fatal("duplicate Create returned nil item — bug reproduced")
	}
	if item.IssueID != 101 {
		t.Errorf("expected IssueID=101, got %d", item.IssueID)
	}
}

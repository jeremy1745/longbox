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
			last_searched_at TEXT,
			procurement_status TEXT NOT NULL DEFAULT 'none' CHECK (procurement_status IN ('none','pending','submitted','acquired','failed')),
			procurement_submitted_at TEXT,
			procurement_last_error TEXT
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

// TestProcurementStatus exercises the full procurement lifecycle:
// default → pending → submitted → failed, plus ListByProcurementStatus.
func TestProcurementStatus(t *testing.T) {
	db := setupWantListTestDB(t)
	defer db.Close()
	repo := NewWantListRepo(db, db)

	item, err := repo.Create(101, 5, "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if item == nil {
		t.Fatal("Create returned nil")
	}

	// Default status must be 'none'.
	if item.ProcurementStatus != "none" {
		t.Errorf("expected default ProcurementStatus='none', got %q", item.ProcurementStatus)
	}
	if item.ProcurementSubmittedAt != nil {
		t.Errorf("expected ProcurementSubmittedAt nil, got %v", item.ProcurementSubmittedAt)
	}
	if item.ProcurementLastError != nil {
		t.Errorf("expected ProcurementLastError nil, got %v", item.ProcurementLastError)
	}

	// Transition to 'pending' (no error).
	if err := repo.SetProcurementStatus(101, "pending", ""); err != nil {
		t.Fatalf("SetProcurementStatus pending: %v", err)
	}
	reloaded, err := repo.GetByIssueID(101)
	if err != nil || reloaded == nil {
		t.Fatalf("GetByIssueID after pending: %v", err)
	}
	if reloaded.ProcurementStatus != "pending" {
		t.Errorf("expected ProcurementStatus='pending', got %q", reloaded.ProcurementStatus)
	}
	if reloaded.ProcurementLastError != nil {
		t.Errorf("expected ProcurementLastError nil after pending, got %v", reloaded.ProcurementLastError)
	}

	// Transition to 'submitted' — should set procurement_submitted_at.
	if err := repo.SetProcurementStatus(101, "submitted", ""); err != nil {
		t.Fatalf("SetProcurementStatus submitted: %v", err)
	}
	reloaded, err = repo.GetByIssueID(101)
	if err != nil || reloaded == nil {
		t.Fatalf("GetByIssueID after submitted: %v", err)
	}
	if reloaded.ProcurementStatus != "submitted" {
		t.Errorf("expected ProcurementStatus='submitted', got %q", reloaded.ProcurementStatus)
	}
	if reloaded.ProcurementSubmittedAt == nil || *reloaded.ProcurementSubmittedAt == "" {
		t.Error("expected ProcurementSubmittedAt to be set after 'submitted' transition")
	}

	// Transition to 'failed' with an error message.
	if err := repo.SetProcurementStatus(101, "failed", "prowlarr unreachable"); err != nil {
		t.Fatalf("SetProcurementStatus failed: %v", err)
	}
	reloaded, err = repo.GetByIssueID(101)
	if err != nil || reloaded == nil {
		t.Fatalf("GetByIssueID after failed: %v", err)
	}
	if reloaded.ProcurementStatus != "failed" {
		t.Errorf("expected ProcurementStatus='failed', got %q", reloaded.ProcurementStatus)
	}
	if reloaded.ProcurementLastError == nil || *reloaded.ProcurementLastError != "prowlarr unreachable" {
		t.Errorf("expected ProcurementLastError='prowlarr unreachable', got %v", reloaded.ProcurementLastError)
	}

	// ListByProcurementStatus("failed") must include this row.
	failed, err := repo.ListByProcurementStatus("failed")
	if err != nil {
		t.Fatalf("ListByProcurementStatus: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("expected 1 failed item, got %d", len(failed))
	}
	if failed[0].IssueID != 101 {
		t.Errorf("expected IssueID=101 in failed list, got %d", failed[0].IssueID)
	}

	// ListByProcurementStatus("none") must return 0 items since issue 101 is now 'failed'.
	noneItems, err := repo.ListByProcurementStatus("none")
	if err != nil {
		t.Fatalf("ListByProcurementStatus none: %v", err)
	}
	if len(noneItems) != 0 {
		t.Errorf("expected 0 'none' items (issue 101 is 'failed'), got %d", len(noneItems))
	}

	// Transition to 'acquired' — terminal success state; submitted_at must be preserved.
	if err := repo.SetProcurementStatus(101, "acquired", ""); err != nil {
		t.Fatalf("SetProcurementStatus acquired: %v", err)
	}
	reloaded, err = repo.GetByIssueID(101)
	if err != nil || reloaded == nil {
		t.Fatalf("GetByIssueID after acquired: %v", err)
	}
	if reloaded.ProcurementStatus != "acquired" {
		t.Errorf("expected ProcurementStatus='acquired', got %q", reloaded.ProcurementStatus)
	}
	// procurement_last_error should be cleared (empty errMsg → NULL).
	if reloaded.ProcurementLastError != nil {
		t.Errorf("expected ProcurementLastError nil after acquired, got %v", reloaded.ProcurementLastError)
	}

	// SetProcurementStatus on a non-existent issueID must return an error.
	if err := repo.SetProcurementStatus(9999, "pending", ""); err == nil {
		t.Error("expected error for unknown issueID, got nil")
	}
}

// TestMarkSeriesPending covers the pull-list refresh promotion: 'none' rows for
// the series become 'pending'; rows already in a later state are left alone.
func TestMarkSeriesPending(t *testing.T) {
	db := setupWantListTestDB(t)
	defer db.Close()
	repo := NewWantListRepo(db, db)

	// Issue 101: a freshly-wanted row, defaults to procurement_status 'none'.
	if _, err := repo.Create(101, 0, ""); err != nil {
		t.Fatalf("Create 101: %v", err)
	}
	// Issue 102: already wanted AND already submitted — must NOT be touched.
	if _, err := repo.Create(102, 0, ""); err != nil {
		t.Fatalf("Create 102: %v", err)
	}
	if err := repo.SetProcurementStatus(102, "submitted", ""); err != nil {
		t.Fatalf("SetProcurementStatus 102: %v", err)
	}

	n, err := repo.MarkSeriesPending(1)
	if err != nil {
		t.Fatalf("MarkSeriesPending: %v", err)
	}
	if n != 1 {
		t.Errorf("expected 1 row promoted, got %d", n)
	}

	got101, err := repo.GetByIssueID(101)
	if err != nil || got101 == nil {
		t.Fatalf("GetByIssueID 101: %v", err)
	}
	if got101.ProcurementStatus != "pending" {
		t.Errorf("issue 101: expected 'pending', got %q", got101.ProcurementStatus)
	}

	got102, err := repo.GetByIssueID(102)
	if err != nil || got102 == nil {
		t.Fatalf("GetByIssueID 102: %v", err)
	}
	if got102.ProcurementStatus != "submitted" {
		t.Errorf("issue 102: expected 'submitted' (untouched), got %q", got102.ProcurementStatus)
	}

	// Idempotent: a second call promotes nothing (no 'none' rows left).
	n2, err := repo.MarkSeriesPending(1)
	if err != nil {
		t.Fatalf("MarkSeriesPending (2nd): %v", err)
	}
	if n2 != 0 {
		t.Errorf("expected 0 rows promoted on second call, got %d", n2)
	}
}

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jeremy/longbox/internal/database"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/prowlarr"
	"github.com/jeremy/longbox/internal/repository"
)

// ── test infrastructure ───────────────────────────────────────────────────

// acqTestEnv bundles a migrated temp-file SQLite DB and the real repos the
// acquisition flow uses. The DB is a real file (not :memory:) so the read and
// write connections database.Open creates both see the same data.
type acqTestEnv struct {
	db           *database.DB
	seriesRepo   *repository.SeriesRepo
	issueRepo    *repository.IssueRepo
	fileRepo     *repository.FileRepo
	wantListRepo *repository.WantListRepo
}

func setupAcqTestEnv(t *testing.T) *acqTestEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "acq_test.db")
	db, err := database.Open(dbPath)
	if err != nil {
		t.Fatalf("opening test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrating test db: %v", err)
	}
	return &acqTestEnv{
		db:           db,
		seriesRepo:   repository.NewSeriesRepo(db.Read, db.Write),
		issueRepo:    repository.NewIssueRepo(db.Read, db.Write),
		fileRepo:     repository.NewFileRepo(db.Read, db.Write),
		wantListRepo: repository.NewWantListRepo(db.Read, db.Write),
	}
}

// seedSeriesWithIssues creates a series and the given issue numbers, returning
// the series and its issues (in creation order).
func (e *acqTestEnv) seedSeriesWithIssues(t *testing.T, title string, year int, issueNumbers ...string) (*model.Series, []model.Issue) {
	t.Helper()
	y := year
	s := &model.Series{Title: title, SortTitle: title, Year: &y, Status: "continuing"}
	if err := e.seriesRepo.Create(s); err != nil {
		t.Fatalf("creating series: %v", err)
	}
	var issues []model.Issue
	for i, num := range issueNumbers {
		iss := &model.Issue{
			SeriesID:    s.ID,
			IssueNumber: num,
			SortNumber:  float64(i + 1),
			ReadStatus:  "unread",
		}
		if err := e.issueRepo.Create(iss); err != nil {
			t.Fatalf("creating issue %s: %v", num, err)
		}
		issues = append(issues, *iss)
	}
	return s, issues
}

// fakeResolver is a seriesResolver that returns a pre-seeded series without
// any ComicVine / Metron network traffic. If err is set, the resolve fails
// with it (used to exercise the conflict-propagation path).
type fakeResolver struct {
	series              *model.Series
	err                 error
	trackFromCVCalled   int
	matchToMetronCalled int
}

func (f *fakeResolver) TrackFromComicVine(cvVolumeID int, wl *repository.WantListRepo, wantAll ...bool) (*model.Series, int, error) {
	f.trackFromCVCalled++
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.series, 0, nil
}

func (f *fakeResolver) MatchSeriesToMetronVolume(ctx context.Context, seriesID int64, metronSeriesID int) error {
	f.matchToMetronCalled++
	return f.err
}

// fakeFolderEnsurer is a folderEnsurer that records calls and optionally
// fails. It does NOT create anything on disk — the test creates the series
// folder itself when it needs the sidecar step to succeed.
type fakeFolderEnsurer struct {
	called int
	err    error
}

func (f *fakeFolderEnsurer) EnsureFolderAndPoster(ctx context.Context, seriesID int64) error {
	f.called++
	return f.err
}

// fakeGrabber is a releaseGrabber backed by per-issue-number canned results.
// searchResults maps an issue number to the releases SearchIssue returns;
// searchErr / grabErr map an issue number to an error to return instead.
type fakeGrabber struct {
	searchResults map[string][]prowlarr.Release
	searchErr     map[string]error
	grabErr       map[string]error
	searchCalls   []string // issue numbers searched, in order
	grabCalls     []string // GUIDs grabbed, in order
}

func newFakeGrabber() *fakeGrabber {
	return &fakeGrabber{
		searchResults: map[string][]prowlarr.Release{},
		searchErr:     map[string]error{},
		grabErr:       map[string]error{},
	}
}

func (f *fakeGrabber) SearchIssue(ctx context.Context, series, issueNumber string, year int) ([]prowlarr.Release, error) {
	f.searchCalls = append(f.searchCalls, issueNumber)
	if err := f.searchErr[issueNumber]; err != nil {
		return nil, err
	}
	return f.searchResults[issueNumber], nil
}

func (f *fakeGrabber) GrabRelease(ctx context.Context, guid string, indexerID int) error {
	f.grabCalls = append(f.grabCalls, guid)
	// grabErr is keyed by GUID here.
	return f.grabErr[guid]
}

// procurementStatus reads the want_list.procurement_status for an issue.
func (e *acqTestEnv) procurementStatus(t *testing.T, issueID int64) string {
	t.Helper()
	item, err := e.wantListRepo.GetByIssueID(issueID)
	if err != nil {
		t.Fatalf("GetByIssueID(%d): %v", issueID, err)
	}
	if item == nil {
		t.Fatalf("no want_list row for issue %d", issueID)
	}
	return item.ProcurementStatus
}

// ── tests ─────────────────────────────────────────────────────────────────

// TestAcquisition_HappyPath exercises the full untracked-series flow:
// resolution → tracked → all issues wanted+pending → folder+sidecar →
// local file moved in → still-missing issues queued via the fake grabber.
func TestAcquisition_HappyPath(t *testing.T) {
	env := setupAcqTestEnv(t)
	libraryDir := t.TempDir()

	// Series with 3 issues. Issue #1 has a loose file in the library that
	// should get moved into the canonical folder; #2 and #3 are missing and
	// should be queued via Prowlarr.
	series, issues := env.seedSeriesWithIssues(t, "Absolute Flash", 2025, "1", "2", "3")

	// A loose file for issue #1, sitting in a non-canonical subdir.
	looseDir := filepath.Join(libraryDir, "incoming")
	if err := os.MkdirAll(looseDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", looseDir, err)
	}
	loosePath := filepath.Join(looseDir, "Absolute Flash 001.cbz")
	makeTestCBZ(t, loosePath, &testComicInfo{Series: "Absolute Flash", Number: "1"})

	// A comic_files row pointing at the loose file — so the move must relink
	// the path AND the issue id.
	cf := &model.ComicFile{
		FilePath:   loosePath,
		FileName:   "Absolute Flash 001.cbz",
		FileFormat: "cbz",
	}
	if err := env.fileRepo.Create(cf); err != nil {
		t.Fatalf("creating comic_files row: %v", err)
	}

	// The folder ensurer is faked, but the sidecar writer is real and needs
	// the directory to exist — create the canonical folder ourselves.
	seriesDir := filepath.Join(libraryDir, buildSeriesFolderName(series.Title, series.Year))
	if err := os.MkdirAll(seriesDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", seriesDir, err)
	}

	resolver := &fakeResolver{series: series}
	folder := &fakeFolderEnsurer{}
	grabber := newFakeGrabber()
	grabber.searchResults["2"] = []prowlarr.Release{{GUID: "guid-2", IndexerID: 7, Title: "Absolute Flash 002"}}
	grabber.searchResults["3"] = []prowlarr.Release{{GUID: "guid-3", IndexerID: 7, Title: "Absolute Flash 003"}}

	svc := NewAcquisitionService(
		resolver, folder,
		NewLibraryScanService(env.seriesRepo),
		grabber,
		env.seriesRepo, env.issueRepo, env.fileRepo, env.wantListRepo,
		libraryDir,
	)

	cvID := int64(12345)
	result, err := svc.WantAndTrackSeries(context.Background(), WantTrackInput{ComicVineID: &cvID})
	if err != nil {
		t.Fatalf("WantAndTrackSeries: unexpected error: %v\nwarnings: %v", err, result.Warnings)
	}

	// Step 1: resolver was used.
	if resolver.trackFromCVCalled != 1 {
		t.Errorf("TrackFromComicVine call count: got %d, want 1", resolver.trackFromCVCalled)
	}
	if result.SeriesID != series.ID {
		t.Errorf("result.SeriesID: got %d, want %d", result.SeriesID, series.ID)
	}

	// Step 2: series tracked, every issue wanted + pending (issue #1 won't
	// stay pending forever — but at no point in this flow is it changed away
	// from pending because it had a local file, so step 5 skipped it).
	reloaded, err := env.seriesRepo.GetByID(series.ID)
	if err != nil || reloaded == nil {
		t.Fatalf("reloading series: %v", err)
	}
	if !reloaded.Tracked {
		t.Error("series should be marked tracked")
	}
	if got := env.procurementStatus(t, issues[0].ID); got != "pending" {
		t.Errorf("issue #1 procurement_status: got %q, want %q (had a local file, not queued)", got, "pending")
	}

	// Step 3: folder ensurer called, sidecar written.
	if folder.called != 1 {
		t.Errorf("EnsureFolderAndPoster call count: got %d, want 1", folder.called)
	}
	if result.FolderPath != seriesDir {
		t.Errorf("result.FolderPath: got %q, want %q", result.FolderPath, seriesDir)
	}
	if !result.MetadataWritten {
		t.Errorf("result.MetadataWritten: got false, want true (warnings: %v)", result.Warnings)
	}
	if !fileExists(filepath.Join(seriesDir, "ComicVine.xml")) {
		t.Error("ComicVine.xml sidecar was not written")
	}

	// Step 4: loose file for #1 moved into the canonical folder, DB relinked.
	if result.FilesMoved != 1 {
		t.Errorf("result.FilesMoved: got %d, want 1 (warnings: %v)", result.FilesMoved, result.Warnings)
	}
	movedPath := filepath.Join(seriesDir, "Absolute Flash 001.cbz")
	if !fileExists(movedPath) {
		t.Errorf("file not at expected canonical path %q", movedPath)
	}
	if fileExists(loosePath) {
		t.Errorf("file still at old loose path %q — should have moved", loosePath)
	}
	relinked, err := env.fileRepo.GetByID(cf.ID)
	if err != nil || relinked == nil {
		t.Fatalf("reloading comic_files row: %v", err)
	}
	if relinked.FilePath != movedPath {
		t.Errorf("comic_files.file_path: got %q, want %q", relinked.FilePath, movedPath)
	}
	if relinked.IssueID == nil || *relinked.IssueID != issues[0].ID {
		t.Errorf("comic_files.issue_id: got %v, want %d", relinked.IssueID, issues[0].ID)
	}

	// Step 5: issues #2 and #3 had no local file → searched + grabbed.
	if result.IssuesQueued != 2 {
		t.Errorf("result.IssuesQueued: got %d, want 2 (warnings: %v)", result.IssuesQueued, result.Warnings)
	}
	if len(grabber.grabCalls) != 2 {
		t.Errorf("grab call count: got %d, want 2", len(grabber.grabCalls))
	}
	if got := env.procurementStatus(t, issues[1].ID); got != "submitted" {
		t.Errorf("issue #2 procurement_status: got %q, want %q", got, "submitted")
	}
	if got := env.procurementStatus(t, issues[2].ID); got != "submitted" {
		t.Errorf("issue #3 procurement_status: got %q, want %q", got, "submitted")
	}
	// Issue #1 should NOT have been searched (it had a local file).
	for _, n := range grabber.searchCalls {
		if n == "1" {
			t.Errorf("issue #1 should not have been searched — it had a local file")
		}
	}
}

// TestAcquisition_ConflictPropagation verifies that a typed match-conflict
// error from the resolver is returned such that errors.As still unwraps to
// the concrete *SeriesMatchConflictError — the Phase 6 handler relies on this
// to emit a 409.
func TestAcquisition_ConflictPropagation(t *testing.T) {
	env := setupAcqTestEnv(t)

	conflict := &SeriesMatchConflictError{
		RequestedSeriesID: 1,
		ConflictingSeries: &model.Series{ID: 99, Title: "Already Here"},
	}
	resolver := &fakeResolver{err: conflict}

	svc := NewAcquisitionService(
		resolver, &fakeFolderEnsurer{},
		NewLibraryScanService(env.seriesRepo),
		newFakeGrabber(),
		env.seriesRepo, env.issueRepo, env.fileRepo, env.wantListRepo,
		t.TempDir(),
	)

	cvID := int64(777)
	_, err := svc.WantAndTrackSeries(context.Background(), WantTrackInput{ComicVineID: &cvID})
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	var typed *SeriesMatchConflictError
	if !errors.As(err, &typed) {
		t.Fatalf("errors.As did not unwrap to *SeriesMatchConflictError; got %T: %v", err, err)
	}
	if typed.ConflictingSeries == nil || typed.ConflictingSeries.ID != 99 {
		t.Errorf("unwrapped conflict lost its payload: %+v", typed)
	}
}

// TestAcquisition_ProwlarrFailurePerIssue verifies that a Prowlarr failure on
// one issue marks just that issue 'failed' and the flow keeps going — the
// remaining issues are still searched and queued.
func TestAcquisition_ProwlarrFailurePerIssue(t *testing.T) {
	env := setupAcqTestEnv(t)
	libraryDir := t.TempDir()

	series, issues := env.seedSeriesWithIssues(t, "Test Title", 2024, "1", "2", "3")
	seriesDir := filepath.Join(libraryDir, buildSeriesFolderName(series.Title, series.Year))
	if err := os.MkdirAll(seriesDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", seriesDir, err)
	}

	resolver := &fakeResolver{series: series}
	grabber := newFakeGrabber()
	// #1 search fails outright.
	grabber.searchErr["1"] = errors.New("indexer timeout")
	// #2 succeeds.
	grabber.searchResults["2"] = []prowlarr.Release{{GUID: "guid-2", IndexerID: 1}}
	// #3 search returns a result but the grab fails.
	grabber.searchResults["3"] = []prowlarr.Release{{GUID: "guid-3", IndexerID: 1}}
	grabber.grabErr["guid-3"] = errors.New("download client rejected")

	svc := NewAcquisitionService(
		resolver, &fakeFolderEnsurer{},
		NewLibraryScanService(env.seriesRepo),
		grabber,
		env.seriesRepo, env.issueRepo, env.fileRepo, env.wantListRepo,
		libraryDir,
	)

	cvID := int64(42)
	result, err := svc.WantAndTrackSeries(context.Background(), WantTrackInput{ComicVineID: &cvID})
	if err != nil {
		t.Fatalf("WantAndTrackSeries: unexpected error: %v", err)
	}

	// All three issues were searched — the flow did not abort on #1's failure.
	if len(grabber.searchCalls) != 3 {
		t.Errorf("search call count: got %d, want 3 (flow should not abort on a per-issue failure)", len(grabber.searchCalls))
	}

	// #1: search failed → 'failed'.
	if got := env.procurementStatus(t, issues[0].ID); got != "failed" {
		t.Errorf("issue #1 procurement_status: got %q, want %q", got, "failed")
	}
	// #2: succeeded → 'submitted'.
	if got := env.procurementStatus(t, issues[1].ID); got != "submitted" {
		t.Errorf("issue #2 procurement_status: got %q, want %q", got, "submitted")
	}
	// #3: grab failed → 'failed'.
	if got := env.procurementStatus(t, issues[2].ID); got != "failed" {
		t.Errorf("issue #3 procurement_status: got %q, want %q", got, "failed")
	}

	// Only #2 actually counts as queued.
	if result.IssuesQueued != 1 {
		t.Errorf("result.IssuesQueued: got %d, want 1", result.IssuesQueued)
	}

	// The failures show up as warnings, with the issue's last-error recorded.
	if len(result.Warnings) < 2 {
		t.Errorf("expected at least 2 warnings for the two failed issues, got %d: %v", len(result.Warnings), result.Warnings)
	}
	item1, _ := env.wantListRepo.GetByIssueID(issues[0].ID)
	if item1 == nil || item1.ProcurementLastError == nil || *item1.ProcurementLastError == "" {
		t.Errorf("issue #1 should have a recorded procurement_last_error, got %+v", item1)
	}
}

// TestAcquisition_NoProwlarrSkipsQueue verifies that a nil prowlarr client
// skips step 5 entirely with a single warning — and crucially does NOT mark
// the missing issues 'failed' (they stay 'pending' from step 2).
func TestAcquisition_NoProwlarrSkipsQueue(t *testing.T) {
	env := setupAcqTestEnv(t)
	libraryDir := t.TempDir()

	series, issues := env.seedSeriesWithIssues(t, "Unqueued Series", 2023, "1", "2")
	seriesDir := filepath.Join(libraryDir, buildSeriesFolderName(series.Title, series.Year))
	if err := os.MkdirAll(seriesDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", seriesDir, err)
	}

	svc := NewAcquisitionService(
		&fakeResolver{series: series}, &fakeFolderEnsurer{},
		NewLibraryScanService(env.seriesRepo),
		nil, // no Prowlarr
		env.seriesRepo, env.issueRepo, env.fileRepo, env.wantListRepo,
		libraryDir,
	)

	cvID := int64(1)
	result, err := svc.WantAndTrackSeries(context.Background(), WantTrackInput{ComicVineID: &cvID})
	if err != nil {
		t.Fatalf("WantAndTrackSeries: unexpected error: %v", err)
	}
	if result.IssuesQueued != 0 {
		t.Errorf("result.IssuesQueued: got %d, want 0", result.IssuesQueued)
	}
	for i := range issues {
		if got := env.procurementStatus(t, issues[i].ID); got != "pending" {
			t.Errorf("issue %s procurement_status: got %q, want %q (Prowlarr off — must stay pending)",
				issues[i].IssueNumber, got, "pending")
		}
	}
	foundSkipWarning := false
	for _, w := range result.Warnings {
		if w == "Prowlarr is not configured — skipping download queue step" {
			foundSkipWarning = true
		}
	}
	if !foundSkipWarning {
		t.Errorf("expected a 'Prowlarr is not configured' warning, got: %v", result.Warnings)
	}
}

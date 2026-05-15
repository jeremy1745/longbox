package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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

// newAcqService builds an AcquisitionService for tests with the background
// launch DISABLED (no-op). Tests drive the background phase explicitly by
// calling svc.completeAcquisition directly, so step 3-5 assertions are
// deterministic and don't race the test's DB teardown.
func newAcqService(
	env *acqTestEnv,
	resolver seriesResolver,
	folder folderEnsurer,
	grabber releaseGrabber,
	libraryDir string,
) *AcquisitionService {
	svc := NewAcquisitionService(
		resolver, folder,
		NewLibraryScanService(env.seriesRepo),
		grabber,
		env.seriesRepo, env.issueRepo, env.fileRepo, env.wantListRepo,
		libraryDir,
	)
	svc.launchBackground = func(func()) {} // tests run the background phase manually
	return svc
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
// folder itself when it needs the sidecar step to succeed. When block is
// non-nil, EnsureFolderAndPoster waits on it — used to prove the background
// phase doesn't block the synchronous WantAndTrackSeries return.
type fakeFolderEnsurer struct {
	called int
	err    error
	block  chan struct{}
}

func (f *fakeFolderEnsurer) EnsureFolderAndPoster(ctx context.Context, seriesID int64) error {
	if f.block != nil {
		<-f.block
	}
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

func warningsContain(warnings []string, substr string) bool {
	for _, w := range warnings {
		if w == substr {
			return true
		}
	}
	return false
}

// ── tests ─────────────────────────────────────────────────────────────────

// TestAcquisition_HappyPath_SyncPhase verifies the synchronous part of the
// flow: resolution → series tracked → every issue wanted + pending. The
// returned result carries SeriesID, IssuesWanted, and BackgroundStarted.
// The slow steps 3-5 are NOT run here (launchBackground is a no-op).
func TestAcquisition_HappyPath_SyncPhase(t *testing.T) {
	env := setupAcqTestEnv(t)
	series, issues := env.seedSeriesWithIssues(t, "Absolute Flash", 2025, "1", "2", "3")

	resolver := &fakeResolver{series: series}
	svc := newAcqService(env, resolver, &fakeFolderEnsurer{}, newFakeGrabber(), t.TempDir())

	cvID := int64(12345)
	result, err := svc.WantAndTrackSeries(context.Background(), WantTrackInput{ComicVineID: &cvID})
	if err != nil {
		t.Fatalf("WantAndTrackSeries: unexpected error: %v\nwarnings: %v", err, result.Warnings)
	}

	if resolver.trackFromCVCalled != 1 {
		t.Errorf("TrackFromComicVine call count: got %d, want 1", resolver.trackFromCVCalled)
	}
	if result.SeriesID != series.ID {
		t.Errorf("result.SeriesID: got %d, want %d", result.SeriesID, series.ID)
	}
	if result.IssuesWanted != 3 {
		t.Errorf("result.IssuesWanted: got %d, want 3", result.IssuesWanted)
	}
	if !result.BackgroundStarted {
		t.Error("result.BackgroundStarted should be true")
	}

	reloaded, err := env.seriesRepo.GetByID(series.ID)
	if err != nil || reloaded == nil {
		t.Fatalf("reloading series: %v", err)
	}
	if !reloaded.Tracked {
		t.Error("series should be marked tracked")
	}
	for i := range issues {
		if got := env.procurementStatus(t, issues[i].ID); got != "pending" {
			t.Errorf("issue %s procurement_status: got %q, want %q", issues[i].IssueNumber, got, "pending")
		}
	}
}

// TestAcquisition_HappyPath_BackgroundPhase verifies the slow steps 3-5 via a
// direct, synchronous completeAcquisition call: folder ensured, sidecar
// written, the loose file for issue #1 moved into the canonical folder and
// relinked, and the still-missing issues #2/#3 queued via the fake grabber.
func TestAcquisition_HappyPath_BackgroundPhase(t *testing.T) {
	env := setupAcqTestEnv(t)
	libraryDir := t.TempDir()
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

	folder := &fakeFolderEnsurer{}
	grabber := newFakeGrabber()
	grabber.searchResults["2"] = []prowlarr.Release{{GUID: "guid-2", IndexerID: 7, Title: "Absolute Flash 002"}}
	grabber.searchResults["3"] = []prowlarr.Release{{GUID: "guid-3", IndexerID: 7, Title: "Absolute Flash 003"}}

	svc := newAcqService(env, &fakeResolver{series: series}, folder, grabber, libraryDir)

	// Sync phase first (wants the issues), then drive the background phase.
	cvID := int64(12345)
	if _, err := svc.WantAndTrackSeries(context.Background(), WantTrackInput{ComicVineID: &cvID}); err != nil {
		t.Fatalf("WantAndTrackSeries (sync): %v", err)
	}
	bg := svc.completeAcquisition(context.Background(), series, issues)

	// Step 3: folder ensurer called, sidecar written.
	if folder.called != 1 {
		t.Errorf("EnsureFolderAndPoster call count: got %d, want 1", folder.called)
	}
	if bg.FolderPath != seriesDir {
		t.Errorf("bg.FolderPath: got %q, want %q", bg.FolderPath, seriesDir)
	}
	if !bg.MetadataWritten {
		t.Errorf("bg.MetadataWritten: got false, want true (warnings: %v)", bg.Warnings)
	}
	if !fileExists(filepath.Join(seriesDir, "ComicVine.xml")) {
		t.Error("ComicVine.xml sidecar was not written")
	}

	// Step 4: loose file for #1 moved into the canonical folder, DB relinked.
	if bg.FilesMoved != 1 {
		t.Errorf("bg.FilesMoved: got %d, want 1 (warnings: %v)", bg.FilesMoved, bg.Warnings)
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
	if bg.IssuesQueued != 2 {
		t.Errorf("bg.IssuesQueued: got %d, want 2 (warnings: %v)", bg.IssuesQueued, bg.Warnings)
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

// TestAcquisition_BackgroundDoesNotBlockSyncPhase proves the sync/async split:
// with a folder ensurer that blocks indefinitely, WantAndTrackSeries must
// still return promptly — the slow steps 3-5 run in a separate goroutine.
func TestAcquisition_BackgroundDoesNotBlockSyncPhase(t *testing.T) {
	env := setupAcqTestEnv(t)
	series, _ := env.seedSeriesWithIssues(t, "Slow Series", 2025, "1")

	folder := &fakeFolderEnsurer{block: make(chan struct{})}
	svc := NewAcquisitionService(
		&fakeResolver{series: series}, folder,
		NewLibraryScanService(env.seriesRepo),
		newFakeGrabber(),
		env.seriesRepo, env.issueRepo, env.fileRepo, env.wantListRepo,
		t.TempDir(),
	)
	// Use a real goroutine launch, but track it so the test can join before
	// teardown closes the DB.
	var bg sync.WaitGroup
	svc.launchBackground = func(fn func()) {
		bg.Add(1)
		go func() { defer bg.Done(); fn() }()
	}

	cvID := int64(1)
	done := make(chan struct{})
	go func() {
		_, err := svc.WantAndTrackSeries(context.Background(), WantTrackInput{ComicVineID: &cvID})
		if err != nil {
			t.Errorf("WantAndTrackSeries: %v", err)
		}
		close(done)
	}()

	select {
	case <-done:
		// Good — returned without waiting on the blocked folder ensurer.
	case <-time.After(3 * time.Second):
		close(folder.block) // unblock so the goroutine can exit
		bg.Wait()
		t.Fatal("WantAndTrackSeries blocked on the background folder ensurer — sync/async split is broken")
	}

	// Unblock the background phase and wait for it so the DB isn't closed
	// out from under it.
	close(folder.block)
	bg.Wait()
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
	svc := newAcqService(env, &fakeResolver{err: conflict}, &fakeFolderEnsurer{}, newFakeGrabber(), t.TempDir())

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
// one issue marks just that issue 'failed' and the background phase keeps
// going — the remaining issues are still searched and queued.
func TestAcquisition_ProwlarrFailurePerIssue(t *testing.T) {
	env := setupAcqTestEnv(t)
	libraryDir := t.TempDir()

	series, issues := env.seedSeriesWithIssues(t, "Test Title", 2024, "1", "2", "3")
	seriesDir := filepath.Join(libraryDir, buildSeriesFolderName(series.Title, series.Year))
	if err := os.MkdirAll(seriesDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", seriesDir, err)
	}

	grabber := newFakeGrabber()
	grabber.searchErr["1"] = errors.New("indexer timeout")                          // #1 search fails outright
	grabber.searchResults["2"] = []prowlarr.Release{{GUID: "guid-2", IndexerID: 1}}  // #2 succeeds
	grabber.searchResults["3"] = []prowlarr.Release{{GUID: "guid-3", IndexerID: 1}}  // #3 result found
	grabber.grabErr["guid-3"] = errors.New("download client rejected")              // ...but grab fails

	svc := newAcqService(env, &fakeResolver{series: series}, &fakeFolderEnsurer{}, grabber, libraryDir)

	cvID := int64(42)
	if _, err := svc.WantAndTrackSeries(context.Background(), WantTrackInput{ComicVineID: &cvID}); err != nil {
		t.Fatalf("WantAndTrackSeries (sync): %v", err)
	}
	bg := svc.completeAcquisition(context.Background(), series, issues)

	// All three issues were searched — the background phase did not abort on
	// #1's failure.
	if len(grabber.searchCalls) != 3 {
		t.Errorf("search call count: got %d, want 3 (must not abort on a per-issue failure)", len(grabber.searchCalls))
	}
	if got := env.procurementStatus(t, issues[0].ID); got != "failed" {
		t.Errorf("issue #1 procurement_status: got %q, want %q", got, "failed")
	}
	if got := env.procurementStatus(t, issues[1].ID); got != "submitted" {
		t.Errorf("issue #2 procurement_status: got %q, want %q", got, "submitted")
	}
	if got := env.procurementStatus(t, issues[2].ID); got != "failed" {
		t.Errorf("issue #3 procurement_status: got %q, want %q", got, "failed")
	}
	if bg.IssuesQueued != 1 {
		t.Errorf("bg.IssuesQueued: got %d, want 1", bg.IssuesQueued)
	}
	if len(bg.Warnings) < 2 {
		t.Errorf("expected at least 2 warnings for the two failed issues, got %d: %v", len(bg.Warnings), bg.Warnings)
	}
	item1, _ := env.wantListRepo.GetByIssueID(issues[0].ID)
	if item1 == nil || item1.ProcurementLastError == nil || *item1.ProcurementLastError == "" {
		t.Errorf("issue #1 should have a recorded procurement_last_error, got %+v", item1)
	}
}

// TestAcquisition_ZeroPaddedIssueNumber verifies Fix C1: when the DB stores an
// issue number as "001" (zero-padded) and the scanned file produces a key that
// normalises to "1", the file IS moved and the issue IS relinked — the
// normalised-map lookup in moveLocalFiles matches them correctly.
func TestAcquisition_ZeroPaddedIssueNumber(t *testing.T) {
	env := setupAcqTestEnv(t)
	libraryDir := t.TempDir()

	series, issues := env.seedSeriesWithIssues(t, "Zero Pad Comics", 2025, "001", "002")

	// Loose file for issue "001" — the ComicInfo reports Number "1" (no pad),
	// so the scan key normalises to "1".
	looseDir := filepath.Join(libraryDir, "incoming")
	if err := os.MkdirAll(looseDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	loosePath := filepath.Join(looseDir, "Zero Pad Comics 001.cbz")
	makeTestCBZ(t, loosePath, &testComicInfo{Series: "Zero Pad Comics", Number: "1"})

	cf := &model.ComicFile{
		FilePath:   loosePath,
		FileName:   "Zero Pad Comics 001.cbz",
		FileFormat: "cbz",
	}
	if err := env.fileRepo.Create(cf); err != nil {
		t.Fatalf("creating comic_files row: %v", err)
	}

	seriesDir := filepath.Join(libraryDir, buildSeriesFolderName(series.Title, series.Year))
	if err := os.MkdirAll(seriesDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	svc := newAcqService(env, &fakeResolver{series: series}, &fakeFolderEnsurer{}, nil, libraryDir)

	cvID := int64(99)
	if _, err := svc.WantAndTrackSeries(context.Background(), WantTrackInput{ComicVineID: &cvID}); err != nil {
		t.Fatalf("WantAndTrackSeries (sync): %v", err)
	}
	bg := svc.completeAcquisition(context.Background(), series, issues)

	if bg.FilesMoved != 1 {
		t.Errorf("bg.FilesMoved: got %d, want 1 (zero-padded issue not matched)\nwarnings: %v", bg.FilesMoved, bg.Warnings)
	}
	movedPath := filepath.Join(seriesDir, "Zero Pad Comics 001.cbz")
	if !fileExists(movedPath) {
		t.Errorf("file not at expected canonical path %q", movedPath)
	}
	if fileExists(loosePath) {
		t.Errorf("file still at old loose path %q — move failed", loosePath)
	}
	relinked, err := env.fileRepo.GetByID(cf.ID)
	if err != nil || relinked == nil {
		t.Fatalf("reloading comic_files row: %v", err)
	}
	if relinked.FilePath != movedPath {
		t.Errorf("comic_files.file_path: got %q, want %q", relinked.FilePath, movedPath)
	}
	if relinked.IssueID == nil || *relinked.IssueID != issues[0].ID {
		t.Errorf("comic_files.issue_id: got %v, want %d (issue 001, id=%d)", relinked.IssueID, issues[0].ID, issues[0].ID)
	}
}

// TestAcquisition_MetronHappyPath exercises the Metron resolution path:
// MetronID → placeholder created → MatchSeriesToMetronVolume succeeds →
// series resolved and tracked. The fake resolver doesn't populate issues, so
// the sync phase wants nothing — we verify the control flow, not metadata.
func TestAcquisition_MetronHappyPath(t *testing.T) {
	env := setupAcqTestEnv(t)
	resolver := &fakeResolver{} // err == nil → success
	svc := newAcqService(env, resolver, &fakeFolderEnsurer{}, nil, t.TempDir())

	metronID := int64(42)
	result, err := svc.WantAndTrackSeries(context.Background(), WantTrackInput{MetronID: &metronID})
	if err != nil {
		t.Fatalf("WantAndTrackSeries (Metron happy path): unexpected error: %v\nwarnings: %v", err, result.Warnings)
	}
	if result.SeriesID == 0 {
		t.Error("result.SeriesID should be non-zero after successful Metron resolution")
	}
	if resolver.matchToMetronCalled != 1 {
		t.Errorf("MatchSeriesToMetronVolume call count: got %d, want 1", resolver.matchToMetronCalled)
	}

	reloaded, err := env.seriesRepo.GetByID(result.SeriesID)
	if err != nil || reloaded == nil {
		t.Fatalf("reloading series after Metron resolve: err=%v series=%v", err, reloaded)
	}
	if !reloaded.Tracked {
		t.Error("series should be marked tracked after Metron happy path")
	}
}

// TestAcquisition_MetronMatchFailureNoLeak verifies that when
// MatchSeriesToMetronVolume fails, the placeholder series row is deleted so no
// orphaned row is left in the DB.
func TestAcquisition_MetronMatchFailureNoLeak(t *testing.T) {
	env := setupAcqTestEnv(t)

	matchErr := errors.New("metron: volume not found")
	resolver := &fakeResolver{err: matchErr}
	svc := newAcqService(env, resolver, &fakeFolderEnsurer{}, nil, t.TempDir())

	metronID := int64(99)
	_, err := svc.WantAndTrackSeries(context.Background(), WantTrackInput{MetronID: &metronID})
	if err == nil {
		t.Fatal("expected an error when MatchSeriesToMetronVolume fails, got nil")
	}
	if !errors.Is(err, matchErr) {
		t.Errorf("error should wrap the match error; got: %v", err)
	}

	all, _, err2 := env.seriesRepo.List(1, 1000, "title", "asc")
	if err2 != nil {
		t.Fatalf("listing series after failed Metron match: %v", err2)
	}
	for _, s := range all {
		if s.Title == "metron-99" {
			t.Errorf("placeholder series %q (id=%d) was not deleted after match failure — leak!", s.Title, s.ID)
		}
	}
	if resolver.matchToMetronCalled != 1 {
		t.Errorf("MatchSeriesToMetronVolume call count: got %d, want 1", resolver.matchToMetronCalled)
	}
}

// TestAcquisition_NoProwlarrSkipsQueue verifies that a nil prowlarr client
// skips step 5 entirely with a single warning — and crucially does NOT mark
// the missing issues 'failed' (they stay 'pending' from the sync phase).
func TestAcquisition_NoProwlarrSkipsQueue(t *testing.T) {
	env := setupAcqTestEnv(t)
	libraryDir := t.TempDir()

	series, issues := env.seedSeriesWithIssues(t, "Unqueued Series", 2023, "1", "2")
	seriesDir := filepath.Join(libraryDir, buildSeriesFolderName(series.Title, series.Year))
	if err := os.MkdirAll(seriesDir, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", seriesDir, err)
	}

	svc := newAcqService(env, &fakeResolver{series: series}, &fakeFolderEnsurer{}, nil, libraryDir)

	cvID := int64(1)
	if _, err := svc.WantAndTrackSeries(context.Background(), WantTrackInput{ComicVineID: &cvID}); err != nil {
		t.Fatalf("WantAndTrackSeries (sync): %v", err)
	}
	bg := svc.completeAcquisition(context.Background(), series, issues)

	if bg.IssuesQueued != 0 {
		t.Errorf("bg.IssuesQueued: got %d, want 0", bg.IssuesQueued)
	}
	for i := range issues {
		if got := env.procurementStatus(t, issues[i].ID); got != "pending" {
			t.Errorf("issue %s procurement_status: got %q, want %q (Prowlarr off — must stay pending)",
				issues[i].IssueNumber, got, "pending")
		}
	}
	if !warningsContain(bg.Warnings, "Prowlarr is not configured — skipping download queue step") {
		t.Errorf("expected a 'Prowlarr is not configured' warning, got: %v", bg.Warnings)
	}
}

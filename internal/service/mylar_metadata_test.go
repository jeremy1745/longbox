package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jeremy/longbox/internal/model"
)

func TestDetermineSeriesFolderPrefersCommonAncestor(t *testing.T) {
	files := []model.ComicFile{
		{FilePath: filepath.Join("/library", "Batman (2025)", "Batman 001.cbz")},
		{FilePath: filepath.Join("/library", "Batman (2025)", "Annuals", "Batman Annual 001.cbz")},
		{FilePath: filepath.Join("/library", "Batman (2025)", "Specials", "Batman Special 001.cbz")},
	}

	got := determineSeriesFolder(files, "/library")
	want := filepath.Join("/library", "Batman (2025)")
	if got != want {
		t.Fatalf("determineSeriesFolder() = %q, want %q", got, want)
	}
}

func TestDetermineSeriesFolderFallsBackToMostCommonParent(t *testing.T) {
	files := []model.ComicFile{
		{FilePath: filepath.Join(".", "Batman 001.cbz")},
		{FilePath: filepath.Join(".", "Batman 002.cbz")},
		{FilePath: filepath.Join("Specials", "Batman Special 001.cbz")},
	}

	got := determineSeriesFolder(files, "")
	if got != "." {
		t.Fatalf("determineSeriesFolder() fallback = %q, want %q", got, ".")
	}
}

func TestDetermineSeriesFolderRefusesLibraryRoot(t *testing.T) {
	// Series files split across two top-level folders under the library root.
	// The naive common ancestor is "/library" which would overwrite another
	// series' sidecar — must fall back to the most-populous subdirectory.
	files := []model.ComicFile{
		{FilePath: filepath.Join("/library", "Batman (2011)", "Batman 001.cbz")},
		{FilePath: filepath.Join("/library", "Batman (2011)", "Batman 002.cbz")},
		{FilePath: filepath.Join("/library", "Batman (2011) Annual", "Batman Annual 001.cbz")},
	}
	got := determineSeriesFolder(files, "/library")
	want := filepath.Join("/library", "Batman (2011)")
	if got != want {
		t.Fatalf("determineSeriesFolder() = %q, want %q (must avoid library root)", got, want)
	}
}

func TestDetermineSeriesFolderReturnsEmptyWhenNoSafeFolder(t *testing.T) {
	// Two files, one per top-level folder. Common ancestor lands at the
	// library root and the most-common parent has no winner that lives below
	// the root, so the function bails.
	files := []model.ComicFile{
		{FilePath: filepath.Join("/library", "Batman 001.cbz")},
		{FilePath: filepath.Join("/library", "Other 001.cbz")},
	}
	got := determineSeriesFolder(files, "/library")
	if got != "" {
		t.Fatalf("determineSeriesFolder() = %q, want empty (no safe folder)", got)
	}
}

func TestMostCommonParentDirIsDeterministic(t *testing.T) {
	files := []model.ComicFile{
		{FilePath: filepath.Join("/library", "A", "x.cbz")},
		{FilePath: filepath.Join("/library", "B", "x.cbz")},
		{FilePath: filepath.Join("/library", "C", "x.cbz")},
	}
	first := mostCommonParentDir(files)
	for i := 0; i < 20; i++ {
		got := mostCommonParentDir(files)
		if got != first {
			t.Fatalf("mostCommonParentDir is non-deterministic: got %q then %q", first, got)
		}
	}
}

func TestReplaceFileIfChangedReplacesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "longbox-series.json")
	tmp := filepath.Join(dir, "longbox-series.json.tmp")

	if err := os.WriteFile(dest, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write dest: %v", err)
	}
	if err := os.WriteFile(tmp, []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write tmp: %v", err)
	}

	changed, err := replaceFileIfChanged(tmp, dest)
	if err != nil {
		t.Fatalf("replaceFileIfChanged() error = %v", err)
	}
	if !changed {
		t.Fatalf("replaceFileIfChanged() changed = false, want true")
	}

	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(data) != "new\n" {
		t.Fatalf("dest contents = %q, want %q", string(data), "new\n")
	}
}

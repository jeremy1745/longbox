package scanner

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jeremy/longbox/internal/archive"
)

// WatchCallback is called when file changes are detected.
// The paths slice contains all changed file paths (deduplicated).
type WatchCallback func(paths []string)

// Watcher monitors a directory tree for new/changed comic files.
type Watcher struct {
	dir      string
	callback WatchCallback
	watcher  *fsnotify.Watcher

	mu       sync.Mutex
	pending  map[string]time.Time
	stopCh   chan struct{}
	debounce time.Duration
}

// NewWatcher creates a file system watcher for the given directory.
func NewWatcher(dir string, callback WatchCallback) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		dir:      dir,
		callback: callback,
		watcher:  fsw,
		pending:  make(map[string]time.Time),
		stopCh:   make(chan struct{}),
		debounce: 3 * time.Second, // Wait 3s after last event before triggering
	}, nil
}

// Start begins watching for file changes.
func (w *Watcher) Start() error {
	// Walk directory tree and add all subdirectories
	err := filepath.Walk(w.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if info.IsDir() {
			// Skip hidden directories
			if strings.HasPrefix(info.Name(), ".") && path != w.dir {
				return filepath.SkipDir
			}
			if err := w.watcher.Add(path); err != nil {
				slog.Warn("failed to watch directory", "path", path, "error", err)
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return err
	}

	slog.Info("file watcher started", "directory", w.dir)

	// Start event processing goroutine
	go w.processEvents()

	// Start debounce timer goroutine
	go w.debounceTicker()

	return nil
}

func (w *Watcher) processEvents() {
	for {
		select {
		case <-w.stopCh:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			slog.Warn("file watcher error", "error", err)
		}
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	// Only care about create and write events
	if !event.Has(fsnotify.Create) && !event.Has(fsnotify.Write) {
		return
	}

	path := event.Name

	// If a new directory was created, watch it too
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if info.IsDir() {
		if !strings.HasPrefix(filepath.Base(path), ".") {
			if err := w.watcher.Add(path); err != nil {
				slog.Warn("failed to watch new directory", "path", path, "error", err)
			}
			slog.Debug("watching new directory", "path", path)
		}
		return
	}

	// Only track comic files
	if !archive.IsComicFile(filepath.Base(path)) {
		return
	}

	// Add to pending with current timestamp (debounce)
	w.mu.Lock()
	w.pending[path] = time.Now()
	w.mu.Unlock()

	slog.Debug("file change detected", "path", path, "op", event.Op)
}

func (w *Watcher) debounceTicker() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ticker.C:
			w.flushPending()
		}
	}
}

func (w *Watcher) flushPending() {
	w.mu.Lock()
	if len(w.pending) == 0 {
		w.mu.Unlock()
		return
	}

	now := time.Now()
	var ready []string
	for path, lastEvent := range w.pending {
		if now.Sub(lastEvent) >= w.debounce {
			ready = append(ready, path)
			delete(w.pending, path)
		}
	}
	w.mu.Unlock()

	if len(ready) > 0 {
		slog.Info("file changes ready for processing", "count", len(ready))
		w.callback(ready)
	}
}

// Restart stops the current watcher and starts watching a new directory.
func (w *Watcher) Restart(newDir string) error {
	// Stop existing goroutines and close fsnotify
	close(w.stopCh)
	w.watcher.Close()

	// Create a fresh fsnotify watcher
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("creating new watcher: %w", err)
	}

	// Reset state
	w.dir = newDir
	w.watcher = fsw
	w.mu.Lock()
	w.pending = make(map[string]time.Time)
	w.mu.Unlock()
	w.stopCh = make(chan struct{})

	return w.Start()
}

// Stop shuts down the file watcher.
func (w *Watcher) Stop() error {
	close(w.stopCh)
	return w.watcher.Close()
}

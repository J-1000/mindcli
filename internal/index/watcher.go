package index

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors directories for file changes and triggers re-indexing.
type Watcher struct {
	indexer      *Indexer
	watcher      *fsnotify.Watcher
	paths        []string
	debounceTime time.Duration
	mu           sync.Mutex
	pending      map[string]time.Time
	done         chan struct{}
}

// NewWatcher creates a file system watcher for the given paths.
func NewWatcher(indexer *Indexer, paths []string) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		indexer:      indexer,
		watcher:      fsWatcher,
		paths:        paths,
		debounceTime: 500 * time.Millisecond,
		pending:      make(map[string]time.Time),
		done:         make(chan struct{}),
	}, nil
}

// Start begins watching for file changes. Blocks until ctx is cancelled.
func (w *Watcher) Start(ctx context.Context) error {
	// Add all directories recursively.
	for _, p := range w.paths {
		path := expandWatchPath(p)
		if err := w.addRecursive(path); err != nil {
			log.Printf("warning: watching %s: %v", path, err)
		}
	}

	// Start debounce goroutine.
	go w.debounceLoop(ctx)

	// Process events.
	for {
		select {
		case <-ctx.Done():
			close(w.done)
			return w.watcher.Close()

		case event, ok := <-w.watcher.Events:
			if !ok {
				return nil
			}
			w.handleEvent(ctx, event)

		case err, ok := <-w.watcher.Errors:
			if !ok {
				return nil
			}
			log.Printf("watcher error: %v", err)
		}
	}
}

// handleEvent processes a file system event.
func (w *Watcher) handleEvent(ctx context.Context, event fsnotify.Event) {
	// Only care about writes, creates, and renames.
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
		if event.Op&fsnotify.Remove != 0 {
			// File removed - schedule removal.
			w.mu.Lock()
			w.pending[event.Name] = time.Now()
			w.mu.Unlock()
		}
		return
	}

	// For new directories, start watching them.
	if event.Op&fsnotify.Create != 0 {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			w.addRecursive(event.Name)
			return
		}
	}

	// Queue file for re-indexing with debounce.
	w.mu.Lock()
	w.pending[event.Name] = time.Now()
	w.mu.Unlock()
}

// debounceLoop periodically processes pending files.
func (w *Watcher) debounceLoop(ctx context.Context) {
	ticker := time.NewTicker(w.debounceTime)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-ticker.C:
			w.processPending(ctx)
		}
	}
}

// processPending re-indexes files that have settled (no changes within debounce window).
func (w *Watcher) processPending(ctx context.Context) {
	w.mu.Lock()
	now := time.Now()
	var ready []string

	for path, lastChange := range w.pending {
		if now.Sub(lastChange) >= w.debounceTime {
			ready = append(ready, path)
		}
	}

	for _, path := range ready {
		delete(w.pending, path)
	}
	w.mu.Unlock()

	for _, path := range ready {
		// Check if file still exists.
		if _, err := os.Stat(path); os.IsNotExist(err) {
			// File was removed.
			if err := w.indexer.RemoveFile(ctx, path); err != nil {
				log.Printf("removing %s from index: %v", path, err)
			}
			continue
		}

		// Re-index the file.
		if err := w.indexer.IndexFile(ctx, path); err != nil {
			log.Printf("re-indexing %s: %v", path, err)
		}
	}
}

// addRecursive adds a directory and all subdirectories to the watcher.
func (w *Watcher) addRecursive(path string) error {
	return filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			// Skip common directories that shouldn't be watched.
			if name == ".git" || name == "node_modules" || name == ".obsidian" {
				return filepath.SkipDir
			}
			return w.watcher.Add(p)
		}
		return nil
	})
}

// expandWatchPath expands ~ to home directory.
func expandWatchPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

package inotify

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// Watcher states
type watchState uint8

const (
	stateNotInitialized watchState = iota
	stateInitialized
	stateWatching
	stateClosed
)

// Watcher handles inotify watching for a directory
type Watcher struct {
	mu       sync.RWMutex
	state    watchState
	close    chan struct{}
	fd       int
	path     string
	watchMap map[int]string
	Events   chan InotifyEvent
}

// watchDir recursively adds watches to the directory and all subdirectories
func (w *Watcher) watchDir(path string) error {
	return filepath.Walk(path, func(walkPath string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if !fi.IsDir() {
			return nil
		}

		w.mu.Lock()
		defer w.mu.Unlock()

		watch, err := unix.InotifyAddWatch(w.fd, walkPath,
			unix.IN_CREATE|unix.IN_DELETE|
				unix.IN_MODIFY|unix.IN_MOVED_FROM|
				unix.IN_MOVED_TO)
		if err != nil {
			return fmt.Errorf("failed to add watch for %s: %w", walkPath, err)
		}

		w.watchMap[watch] = walkPath
		slog.Debug("watchDir", slog.String("path", walkPath))
		return nil
	})
}

// Watch starts watching for file system events
func (w *Watcher) Watch() error {
	w.mu.Lock()
	if w.state != stateInitialized {
		w.mu.Unlock()
		return fmt.Errorf("invalid state: %d", w.state)
	}
	w.state = stateWatching
	w.mu.Unlock()

	defer func() {
		w.mu.Lock()
		unix.Close(w.fd)
		w.state = stateClosed
		w.mu.Unlock()
		w.Events <- InotifyEvent{Event: WatchStop}
		close(w.Events)
	}()

	buf := make([]byte, 4096)
	for {
		select {
		case <-w.close:
			return nil
		default:
			n, err := unix.Read(w.fd, buf)
			if err != nil {
				return fmt.Errorf("read error: %w", err)
			}

			if err := w.parseEvents(buf[:n]); err != nil {
				return fmt.Errorf("parse error: %w", err)
			}
		}
	}
}

// Close stops the watcher
func (w *Watcher) Close() error {

	w.mu.RLock()
	defer w.mu.RUnlock()

	if w.state != stateWatching {
		slog.Debug("Watch", slog.String("action", "close"), slog.String("error", "not watching"))
		return fmt.Errorf("watcher is not in watching state")
	}
	slog.Debug("Watch", slog.String("func", "close socket"))
	w.Events <- InotifyEvent{Event: WatchStop}
	w.state = stateClosed

	close(w.close)
	return nil
}

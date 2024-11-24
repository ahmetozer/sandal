package inotify

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// Event types
type EventType uint8

const (
	FolderCreate EventType = iota
	FileCreate
	Delete
	Modified
	MovedFrom
	MovedTo
	WatchStop
)

// Watcher states
type watchState uint8

const (
	stateNotInitialized watchState = iota
	stateInitialized
	stateWatching
	stateClosed
)

const (
	// The size of an inotify event excluding the name
	inotifyEventBaseSize = 16 // 4 + 4 + 4 + 4 bytes for wd, mask, cookie, len
)

// InotifyEvent represents a file system event
type InotifyEvent struct {
	Path  string
	Event EventType
}

// systemInotifyEvent represents the structure of the inotify_event from the Linux kernel
type systemInotifyEvent struct {
	Wd     int32
	Mask   uint32
	Cookie uint32
	Len    uint32
}

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

// New creates a new inotify watcher for the specified path
func New(path string) (*Watcher, error) {
	w := &Watcher{
		path:     path,
		state:    stateNotInitialized,
		close:    make(chan struct{}),
		watchMap: make(map[int]string),
		Events:   make(chan InotifyEvent, 100), // Added buffer to prevent blocking
	}

	fd, err := unix.InotifyInit()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize inotify: %w", err)
	}
	w.fd = fd

	if err := w.watchDir(path); err != nil {
		unix.Close(fd)
		return nil, err
	}

	w.state = stateInitialized
	slog.Debug("New", slog.String("action", "watch initialized"), slog.String("path", path))
	return w, nil
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

// parseEvents processes the raw event data from the buffer
func (w *Watcher) parseEvents(buf []byte) error {
	var offset int
	for offset < len(buf) {
		if offset+inotifyEventBaseSize > len(buf) {
			return fmt.Errorf("insufficient buffer size")
		}

		var event systemInotifyEvent
		if err := binary.Read(bytes.NewReader(buf[offset:offset+inotifyEventBaseSize]), binary.LittleEndian, &event); err != nil {
			return fmt.Errorf("failed to read event: %w", err)
		}

		name := ""
		if event.Len > 0 {
			nameBytes := buf[offset+inotifyEventBaseSize : offset+inotifyEventBaseSize+int(event.Len)]
			name = string(bytes.TrimRight(nameBytes, "\x00"))
		}

		w.mu.RLock()
		dirPath, ok := w.watchMap[int(event.Wd)]
		w.mu.RUnlock()
		if !ok {
			return fmt.Errorf("unknown watch descriptor: %d", event.Wd)
		}

		fullPath := filepath.Join(dirPath, name)

		if err := w.handleEvent(event, fullPath); err != nil {
			slog.Warn("parseEvents", slog.String("action", "handleEvent"), slog.Any("error", err))
		}

		offset += inotifyEventBaseSize + int(event.Len)
	}
	return nil
}

// handleEvent processes individual inotify events
func (w *Watcher) handleEvent(event systemInotifyEvent, fullPath string) error {
	switch {
	case event.Mask&unix.IN_CREATE != 0:
		if fi, err := os.Stat(fullPath); err == nil && fi.IsDir() {
			if err := w.watchDir(fullPath); err != nil {
				return fmt.Errorf("failed to watch new directory %s: %w", fullPath, err)
			}
			w.Events <- InotifyEvent{Path: fullPath, Event: FolderCreate}
		} else {
			w.Events <- InotifyEvent{Path: fullPath, Event: FileCreate}
		}
	case event.Mask&unix.IN_DELETE != 0:
		w.Events <- InotifyEvent{Path: fullPath, Event: Delete}
	case event.Mask&unix.IN_MODIFY != 0:
		w.Events <- InotifyEvent{Path: fullPath, Event: Modified}
	case event.Mask&unix.IN_MOVED_FROM != 0:
		w.Events <- InotifyEvent{Path: fullPath, Event: MovedFrom}
	case event.Mask&unix.IN_MOVED_TO != 0:
		w.Events <- InotifyEvent{Path: fullPath, Event: MovedTo}
	}
	return nil
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

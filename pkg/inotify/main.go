package inotify

import (
	"fmt"
	"log/slog"

	"golang.org/x/sys/unix"
)

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

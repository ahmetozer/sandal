//go:build linux

package inotify

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

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

		// IN_Q_OVERFLOW (delivered with Wd = -1) means the kernel dropped
		// events because the queue filled. It must NOT be fatal: returning an
		// error here propagates out of Watch() and kills the watcher for the
		// daemon's lifetime, after which every removal is missed and startup
		// containers get resurrected (audit L8). Skip it; the daemon's
		// disk-authoritative reconcile recovers any state missed during the
		// overflow burst.
		if event.Mask&unix.IN_Q_OVERFLOW != 0 {
			slog.Warn("parseEvents", slog.String("event", "IN_Q_OVERFLOW"),
				slog.String("action", "queue overflowed; events dropped, will reconcile"))
			offset += inotifyEventBaseSize + int(event.Len)
			continue
		}

		w.mu.RLock()
		dirPath, ok := w.watchMap[int(event.Wd)]
		w.mu.RUnlock()
		if !ok {
			// Unknown watch descriptor (e.g. a watch already removed, or a
			// stray sentinel). Skip rather than killing the whole watcher.
			slog.Debug("parseEvents", slog.String("action", "skip unknown watch descriptor"),
				slog.Int("wd", int(event.Wd)))
			offset += inotifyEventBaseSize + int(event.Len)
			continue
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

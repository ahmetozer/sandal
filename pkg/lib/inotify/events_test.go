//go:build linux

package inotify

import (
	"bytes"
	"encoding/binary"
	"testing"

	"golang.org/x/sys/unix"
)

// encodeInotifyEvent builds a raw inotify_event record (header + null-padded
// name) the way the kernel lays it out, so parseEvents can be exercised
// without a live inotify fd.
func encodeInotifyEvent(wd int32, mask, cookie uint32, name string) []byte {
	var nameBytes []byte
	if name != "" {
		nameBytes = append([]byte(name), 0)
		for len(nameBytes)%4 != 0 {
			nameBytes = append(nameBytes, 0)
		}
	}
	hdr := systemInotifyEvent{Wd: wd, Mask: mask, Cookie: cookie, Len: uint32(len(nameBytes))}
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, hdr)
	b.Write(nameBytes)
	return b.Bytes()
}

// An IN_Q_OVERFLOW sentinel (wd = -1) must not be fatal: previously parseEvents
// returned "unknown watch descriptor" for it, which propagated out of Watch()
// and killed the watcher permanently (audit L8).
func TestParseEventsOverflowNonFatal(t *testing.T) {
	w := &Watcher{watchMap: map[int]string{}, Events: make(chan InotifyEvent, 8)}
	buf := encodeInotifyEvent(-1, unix.IN_Q_OVERFLOW, 0, "")
	if err := w.parseEvents(buf); err != nil {
		t.Fatalf("IN_Q_OVERFLOW must be non-fatal, got error: %v", err)
	}
}

// An event for a watch descriptor we don't know (e.g. a watch already removed)
// must be skipped, not fatal.
func TestParseEventsUnknownWdNonFatal(t *testing.T) {
	w := &Watcher{watchMap: map[int]string{}, Events: make(chan InotifyEvent, 8)}
	buf := encodeInotifyEvent(999, unix.IN_DELETE, 0, "x.json")
	if err := w.parseEvents(buf); err != nil {
		t.Fatalf("unknown watch descriptor must be non-fatal, got error: %v", err)
	}
}

// A known wd must still be parsed and dispatched to Events.
func TestParseEventsKnownWdDispatches(t *testing.T) {
	w := &Watcher{watchMap: map[int]string{7: "/state"}, Events: make(chan InotifyEvent, 8)}
	buf := encodeInotifyEvent(7, unix.IN_DELETE, 0, "web.app.json")
	if err := w.parseEvents(buf); err != nil {
		t.Fatalf("known wd parse error: %v", err)
	}
	select {
	case ev := <-w.Events:
		if ev.Event != Delete || ev.Path != "/state/web.app.json" {
			t.Fatalf("unexpected event: %+v", ev)
		}
	default:
		t.Fatal("expected a Delete event to be dispatched")
	}
}

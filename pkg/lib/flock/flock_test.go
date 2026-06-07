//go:build linux || darwin

package flock

import (
	"path/filepath"
	"testing"
)

// flock is per open-file-description: two independent opens of the same path
// (even in the same process) contend. This guards the property the per-name
// lifecycle lock relies on for cross-process mutual exclusion.
func TestTryLockContended(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.lock")

	a, err := Open(path)
	if err != nil {
		t.Fatalf("Open a: %v", err)
	}
	if err := a.Lock(); err != nil {
		t.Fatalf("a.Lock: %v", err)
	}

	b, err := Open(path)
	if err != nil {
		t.Fatalf("Open b: %v", err)
	}
	if ok, err := b.TryLock(); err != nil || ok {
		t.Fatalf("b.TryLock while a holds = (%v,%v), want (false,nil)", ok, err)
	}

	if err := a.Unlock(); err != nil {
		t.Fatalf("a.Unlock: %v", err)
	}
	if ok, err := b.TryLock(); err != nil || !ok {
		t.Fatalf("b.TryLock after a released = (%v,%v), want (true,nil)", ok, err)
	}
	if err := b.Unlock(); err != nil {
		t.Fatalf("b.Unlock: %v", err)
	}
}

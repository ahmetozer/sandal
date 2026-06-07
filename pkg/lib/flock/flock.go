//go:build linux || darwin

// Package flock is a thin advisory whole-file lock (flock(2)) wrapper. The lock
// is associated with the open file description, so it is released when the file
// is closed or the process dies — there are no stale locks to clean up. flock
// is cross-process: independent processes that lock the same path are mutually
// excluded, which is what the per-name container lifecycle lock relies on.
package flock

import (
	"os"

	"golang.org/x/sys/unix"
)

// Lock holds an flock on an open file. Not safe for concurrent use from
// multiple goroutines; callers serialize their own access.
type Lock struct {
	f *os.File
}

// Open creates/opens path and returns an unlocked Lock. Call Lock or TryLock to
// acquire, then Unlock to release (which also closes the file).
func Open(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	return &Lock{f: f}, nil
}

// Lock blocks until the exclusive lock is held.
func (l *Lock) Lock() error {
	return unix.Flock(int(l.f.Fd()), unix.LOCK_EX)
}

// TryLock attempts to take the exclusive lock without blocking. ok is false if
// another holder owns it.
func (l *Lock) TryLock() (ok bool, err error) {
	err = unix.Flock(int(l.f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if err == unix.EWOULDBLOCK {
		return false, nil
	}
	return false, err
}

// Unlock releases the lock and closes the underlying file.
func (l *Lock) Unlock() error {
	unlockErr := unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	closeErr := l.f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

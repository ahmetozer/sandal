//go:build linux || darwin

// Package namelock provides a per-container-name lifecycle lock used to
// serialize placement and teardown (run / recover / kill / rm) across every
// process — the CLI and the daemon — and across the daemon's own goroutines.
//
// It is backed by an flock on <RunDir>/locks/<name>.lock. Because flock is tied
// to the open file description, the lock is released automatically if the holder
// process dies, so a crashed CLI or daemon never leaves a name wedged.
//
// Ownership contract: acquire ONCE at the outermost lifecycle entry
// (RunContainer, contRecover, KillByName, rm/clear) and hold it across the whole
// critical section. Inner helpers (Run, crun, DeRunContainer, Kill) assume the
// lock is held and must NOT re-acquire it: flock treats two independent opens in
// the same process as contending, so a nested Acquire would deadlock.
package namelock

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/flock"
)

const pollInterval = 50 * time.Millisecond

// DefaultTimeout bounds how long a blocking Acquire waits before failing. Long
// enough to ride out a brief teardown by another actor, short enough that a CLI
// command doesn't hang indefinitely behind a stuck holder.
const DefaultTimeout = 30 * time.Second

// releaseOnce wraps Unlock so a release func can be called more than once
// safely (e.g. an explicit release followed by a deferred release).
func releaseOnce(l *flock.Lock) func() {
	var once sync.Once
	return func() { once.Do(func() { _ = l.Unlock() }) }
}

func lockDir() string { return filepath.Join(env.RunDir, "locks") }

func open(name string) (*flock.Lock, error) {
	dir := lockDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create lock dir %q: %w", dir, err)
	}
	return flock.Open(filepath.Join(dir, name+".lock"))
}

// TryAcquire takes the lifecycle lock for name without blocking. On success it
// returns a release func and ok=true; if another holder owns the name it returns
// (nil, false, nil). The caller must call release exactly once when done.
func TryAcquire(name string) (release func(), ok bool, err error) {
	l, err := open(name)
	if err != nil {
		return nil, false, err
	}
	got, err := l.TryLock()
	if err != nil {
		_ = l.Unlock()
		return nil, false, err
	}
	if !got {
		_ = l.Unlock()
		return nil, false, nil
	}
	return releaseOnce(l), true, nil
}

// Acquire blocks until the lifecycle lock for name is held or timeout elapses.
// A negative timeout blocks indefinitely. On success it returns a release func
// the caller must invoke exactly once.
func Acquire(name string, timeout time.Duration) (release func(), err error) {
	l, err := open(name)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		got, lerr := l.TryLock()
		if lerr != nil {
			_ = l.Unlock()
			return nil, lerr
		}
		if got {
			return releaseOnce(l), nil
		}
		if timeout >= 0 && !time.Now().Before(deadline) {
			_ = l.Unlock()
			return nil, fmt.Errorf("timeout acquiring lifecycle lock for %q after %s", name, timeout)
		}
		time.Sleep(pollInterval)
	}
}

//go:build linux

package daemon

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

// fakeRecovery replaces the recovery and the clock: each recovery is
// recorded at the fake time it was dispatched.
type fakeRecovery struct {
	mu    sync.Mutex
	start time.Time
	now   time.Time
	at    []time.Duration
}

func useFakeRecovery(t *testing.T) *fakeRecovery {
	t.Helper()
	f := &fakeRecovery{start: time.Unix(1_800_000_000, 0)}
	f.now = f.start
	prevRecover, prevNow := recoverContainer, recoveryNow
	recoverContainer = func(*config.Config) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.at = append(f.at, f.now.Sub(f.start))
	}
	recoveryNow = func() time.Time {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.now
	}
	t.Cleanup(func() { recoverContainer, recoveryNow = prevRecover, prevNow })
	return f
}

// tick is one health-check pass that finds cont dead.
func (f *fakeRecovery) tick(cont *config.Config) {
	dispatchRecovery(cont)
	recoveryWG.Wait()
	f.mu.Lock()
	f.now = f.now.Add(3 * time.Second)
	f.mu.Unlock()
}

func seconds(s ...int) []time.Duration {
	out := make([]time.Duration, len(s))
	for i, v := range s {
		out[i] = time.Duration(v) * time.Second
	}
	return out
}

// A container that exits right after every recovery was recovered on every
// 3-second tick, each time mounting its image and change dir again. The wait
// doubles instead, up to a minute.
func TestDispatchRecoveryBacksOffACrashLoop(t *testing.T) {
	f := useFakeRecovery(t)
	cont := &config.Config{Name: "rr-crash", Startup: true, Created: 1}
	for i := 0; i <= 100; i++ { // 300 s, dead on every tick
		f.tick(cont)
	}
	if want := seconds(0, 6, 15, 30, 57, 108, 171, 234, 297); !slices.Equal(f.at, want) {
		t.Fatalf("recoveries at %v, want %v", f.at, want)
	}
}

// One that ran for a while is recovered on the first tick that finds it
// dead, and so is a new run of the same name.
func TestDispatchRecoveryAfterAHealthyRun(t *testing.T) {
	f := useFakeRecovery(t)
	cont := &config.Config{Name: "rr-flaky", Startup: true, Created: 1}
	f.tick(cont)              // recovered at 0 s
	f.tick(cont)              // died at once: next one at 6 s
	f.tick(cont)              // 6 s
	for i := 0; i < 20; i++ { // ran for a minute
		f.mu.Lock()
		f.now = f.now.Add(3 * time.Second)
		f.mu.Unlock()
	}
	f.tick(cont) // 69 s: died after a minute
	f.tick(cont) // died at once again: waits 3 s
	rerun := &config.Config{Name: "rr-flaky", Startup: true, Created: 2}
	f.tick(rerun) // 75 s: a new run of the name starts over
	if want := seconds(0, 6, 69, 75); !slices.Equal(f.at, want) {
		t.Fatalf("recoveries at %v, want %v", f.at, want)
	}
}

// A stopped container is never recovered, so it must not count as a crash.
func TestDispatchRecoverySkipsStopped(t *testing.T) {
	f := useFakeRecovery(t)
	cont := &config.Config{Name: "rr-stopped", Startup: true, Created: 1, Status: "stop"}
	for i := 0; i < 5; i++ {
		f.tick(cont)
	}
	if len(f.at) != 0 {
		t.Fatalf("recoveries at %v, want none", f.at)
	}
}

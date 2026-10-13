//go:build linux

package renumber

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSource struct {
	events  chan *net.IPNet
	stopped atomic.Bool
}

func newFakeSource(buffer int) *fakeSource {
	return &fakeSource{events: make(chan *net.IPNet, buffer)}
}

func (s *fakeSource) Run(ctx context.Context) <-chan *net.IPNet {
	return s.events
}

func (s *fakeSource) Stop() {
	s.stopped.Store(true)
}

type fakeApplier struct {
	mu      sync.Mutex
	calls   []*net.IPNet
	hook    func()
	errOnce error
	errSent bool
}

func (a *fakeApplier) Apply(ctx context.Context, prefix *net.IPNet) error {
	a.mu.Lock()
	a.calls = append(a.calls, prefix)
	hook := a.hook
	err := a.errOnce
	if err != nil && !a.errSent {
		a.errSent = true
		a.mu.Unlock()
		if hook != nil {
			hook()
		}
		return err
	}
	a.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func (a *fakeApplier) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.calls)
}

func (a *fakeApplier) calledWith() []*net.IPNet {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]*net.IPNet, len(a.calls))
	copy(out, a.calls)
	return out
}

func mustCIDRString(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestServiceAppliesEachDistinctPrefix(t *testing.T) {
	src := newFakeSource(4)
	app := &fakeApplier{}
	svc := NewService(src, app, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)

	p1 := mustCIDRString(t, "2001:db8:1::/64")
	p2 := mustCIDRString(t, "2001:db8:2::/64")

	// Send p1 and wait for its Apply to complete before sending p2.
	// The drain debounce window would otherwise coalesce p1 and p2 into one.
	src.events <- p1
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if app.callCount() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := app.callCount(); got != 1 {
		t.Fatalf("want 1 Apply call after p1, got %d", got)
	}

	src.events <- p2
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if app.callCount() == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := app.callCount(); got != 2 {
		t.Fatalf("want 2 Apply calls total, got %d", got)
	}
}

func TestServiceCoalescesDuplicates(t *testing.T) {
	src := newFakeSource(4)
	app := &fakeApplier{}
	svc := NewService(src, app, 10*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)

	p := mustCIDRString(t, "2001:db8::/64")
	src.events <- p
	// Wait for the first apply to settle.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if app.callCount() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := app.callCount(); got != 1 {
		t.Fatalf("want 1 Apply call after first event, got %d", got)
	}
	// Send an identical event — should NOT trigger another Apply because
	// current == p inside the service.
	src.events <- p
	time.Sleep(80 * time.Millisecond)
	if got := app.callCount(); got != 1 {
		t.Fatalf("want still 1 Apply after duplicate, got %d", got)
	}
}

func TestServiceSingleFlightCoalescesInFlight(t *testing.T) {
	src := newFakeSource(4)
	gate := make(chan struct{})
	app := &fakeApplier{
		hook: func() { <-gate },
	}
	svc := NewService(src, app, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)

	p1 := mustCIDRString(t, "2001:db8:1::/64")
	p2 := mustCIDRString(t, "2001:db8:2::/64")
	p3 := mustCIDRString(t, "2001:db8:3::/64")

	src.events <- p1
	// Wait for first Apply to enter the hook.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if app.callCount() == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := app.callCount(); got != 1 {
		t.Fatalf("want 1 Apply in flight, got %d", got)
	}
	// While Apply is blocked in the hook, push two more events. Only the
	// latest should trigger a follow-up Apply.
	src.events <- p2
	src.events <- p3
	// Release the first Apply.
	gate <- struct{}{}
	// Release the follow-up Apply.
	gate <- struct{}{}
	// Drain remaining hook signals so the test can finish.
	go func() {
		for {
			select {
			case <-gate:
			case <-ctx.Done():
				return
			}
		}
	}()

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if app.callCount() == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls := app.calledWith()
	if len(calls) != 2 {
		t.Fatalf("want 2 Apply calls total (first + coalesced follow-up), got %d", len(calls))
	}
	// Second call should be p3, not p2.
	if calls[1].String() != p3.String() {
		t.Errorf("want second Apply with p3=%s, got %s", p3, calls[1])
	}
}

func waitCalls(t *testing.T, app *fakeApplier, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if app.callCount() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("want %d Apply calls, got %d", n, app.callCount())
}

func TestServiceRetriesAfterApplyError(t *testing.T) {
	p1 := mustCIDRString(t, "2001:db8:1::/64")
	p2 := mustCIDRString(t, "2001:db8:2::/64")

	t.Run("same prefix retried without a new event", func(t *testing.T) {
		src := newFakeSource(4)
		app := &fakeApplier{errOnce: errors.New("apply boom")}
		svc := NewService(src, app, 5*time.Millisecond)
		svc.retryBackoff = 10 * time.Millisecond

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go svc.Run(ctx)

		src.events <- p1
		waitCalls(t, app, 2)
		calls := app.calledWith()
		if calls[0].String() != p1.String() || calls[1].String() != p1.String() {
			t.Fatalf("Apply calls = %v, want [%s %s]", calls, p1, p1)
		}
		time.Sleep(50 * time.Millisecond)
		if got := app.callCount(); got != 2 {
			t.Fatalf("Apply called %d times after successful retry, want 2", got)
		}
	})

	t.Run("newer event supersedes the retry", func(t *testing.T) {
		src := newFakeSource(4)
		app := &fakeApplier{errOnce: errors.New("apply boom")}
		svc := NewService(src, app, 5*time.Millisecond)
		svc.retryBackoff = 100 * time.Millisecond

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go svc.Run(ctx)

		src.events <- p1
		waitCalls(t, app, 1)
		// p2 arrives while the drain waits out the backoff for p1.
		src.events <- p2
		waitCalls(t, app, 2)
		time.Sleep(150 * time.Millisecond)
		calls := app.calledWith()
		if len(calls) != 2 || calls[1].String() != p2.String() {
			t.Fatalf("Apply calls = %v, want [%s %s]", calls, p1, p2)
		}
	})

	t.Run("revert to the applied prefix supersedes the retry", func(t *testing.T) {
		src := newFakeSource(4)
		app := &fakeApplier{}
		svc := NewService(src, app, 5*time.Millisecond)
		svc.retryBackoff = 100 * time.Millisecond

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go svc.Run(ctx)

		src.events <- p1
		waitCalls(t, app, 1)
		app.mu.Lock()
		app.errOnce = errors.New("apply boom")
		app.mu.Unlock()
		src.events <- p2
		waitCalls(t, app, 2)
		// The failed apply may have left p2 half-installed, so p1 coming
		// back must be applied again rather than deduplicated.
		src.events <- p1
		waitCalls(t, app, 3)
		time.Sleep(150 * time.Millisecond)
		calls := app.calledWith()
		if len(calls) != 3 || calls[2].String() != p1.String() {
			t.Fatalf("Apply calls = %v, want [%s %s %s]", calls, p1, p2, p1)
		}
	})
}

func TestServiceCancelStopsSource(t *testing.T) {
	src := newFakeSource(0)
	app := &fakeApplier{}
	svc := NewService(src, app, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Service.Run did not return after ctx cancel")
	}
	if !src.stopped.Load() {
		t.Error("expected source.Stop() to be called on shutdown")
	}
}

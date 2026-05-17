package dhcp

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeRefresher struct {
	mu sync.Mutex

	renews   atomic.Int32
	rebinds  atomic.Int32
	released atomic.Bool

	renewErr  error
	rebindErr error

	t1, t2, valid time.Duration
}

func (f *fakeRefresher) Renew(ctx context.Context) (time.Duration, time.Duration, time.Duration, error) {
	f.renews.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t1, f.t2, f.valid, f.renewErr
}

func (f *fakeRefresher) Rebind(ctx context.Context) (time.Duration, time.Duration, time.Duration, error) {
	f.rebinds.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t1, f.t2, f.valid, f.rebindErr
}

func (f *fakeRefresher) Release() error {
	f.released.Store(true)
	return nil
}

func TestLeaseLoopRenewsAtT1(t *testing.T) {
	r := &fakeRefresher{
		t1:    50 * time.Millisecond,
		t2:    200 * time.Millisecond,
		valid: 500 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop := NewLeaseLoop(r, LeaseTimes{T1: 50 * time.Millisecond, T2: 200 * time.Millisecond, Valid: 500 * time.Millisecond})
	go loop.Run(ctx)
	time.Sleep(170 * time.Millisecond)
	cancel()
	loop.Wait()
	if r.renews.Load() < 2 {
		t.Errorf("expected ≥2 renews in 170ms with T1=50ms, got %d", r.renews.Load())
	}
	if r.rebinds.Load() != 0 {
		t.Errorf("expected 0 rebinds while renew is succeeding, got %d", r.rebinds.Load())
	}
	if !r.released.Load() {
		t.Error("expected Release on cancel")
	}
}

func TestLeaseLoopRebindsWhenRenewFails(t *testing.T) {
	r := &fakeRefresher{
		t1:       30 * time.Millisecond,
		t2:       80 * time.Millisecond,
		valid:    300 * time.Millisecond,
		renewErr: context.DeadlineExceeded,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	loop := NewLeaseLoop(r, LeaseTimes{T1: 30 * time.Millisecond, T2: 80 * time.Millisecond, Valid: 300 * time.Millisecond})
	go loop.Run(ctx)
	time.Sleep(150 * time.Millisecond)
	cancel()
	loop.Wait()
	if r.rebinds.Load() == 0 {
		t.Error("expected ≥1 rebind after renew failed past T2")
	}
}

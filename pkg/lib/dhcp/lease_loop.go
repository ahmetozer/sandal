package dhcp

import (
	"context"
	"log/slog"
	"time"
)

// LeaseRefresher is the contract a lease-loop driver expects from the underlying
// DHCPv6 client. Implementations exist for IA_NA (in-container) and IA_PD (host-side).
//
// Renew/Rebind return the next round's T1, T2, and ValidLifetime. They MUST NOT
// block past the supplied context.
type LeaseRefresher interface {
	Renew(ctx context.Context) (t1, t2, valid time.Duration, err error)
	Rebind(ctx context.Context) (t1, t2, valid time.Duration, err error)
	Release() error
}

// LeaseTimes is the initial timing snapshot from the first lease.
type LeaseTimes struct {
	T1, T2, Valid time.Duration
}

// LeaseLoop drives Renew → Rebind → Release for a single binding.
type LeaseLoop struct {
	r       LeaseRefresher
	initial LeaseTimes
	done    chan struct{}
}

// NewLeaseLoop constructs a loop. Caller must invoke Run in a goroutine and
// Wait when ready to join.
func NewLeaseLoop(r LeaseRefresher, initial LeaseTimes) *LeaseLoop {
	return &LeaseLoop{
		r:       r,
		initial: initial,
		done:    make(chan struct{}),
	}
}

// Run blocks until ctx is canceled. Sends Release on exit.
func (l *LeaseLoop) Run(ctx context.Context) {
	defer close(l.done)
	defer func() {
		if err := l.r.Release(); err != nil {
			slog.Debug("dhcp6: release", "err", err)
		}
	}()

	t1, t2, valid := defaultsFor(l.initial)
	t1Deadline := time.Now().Add(t1)
	t2Deadline := time.Now().Add(t2)
	validDeadline := time.Now().Add(valid)

	for {
		now := time.Now()
		// Phase 1: until T1, just sleep.
		if d := t1Deadline.Sub(now); d > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(d):
			}
		}
		// Phase 2: between T1 and T2 — keep trying Renew.
		renewedOK := false
		for {
			now = time.Now()
			if !now.Before(t2Deadline) {
				break
			}
			renewCtx, renewCancel := context.WithDeadline(ctx, minTime(t2Deadline, now.Add(2*time.Second)))
			nt1, nt2, nv, err := l.r.Renew(renewCtx)
			renewCancel()
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				t1, t2, valid = defaultsFor(LeaseTimes{T1: nt1, T2: nt2, Valid: nv})
				t1Deadline = time.Now().Add(t1)
				t2Deadline = time.Now().Add(t2)
				validDeadline = time.Now().Add(valid)
				renewedOK = true
				break
			}
			slog.Debug("dhcp6: renew failed, retrying", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryBackoff(t1, t2)):
			}
		}
		if renewedOK {
			continue
		}
		// Phase 3: T2 reached without Renew success — Rebind until valid lifetime expires.
		rebindOK := false
		for {
			now = time.Now()
			if !now.Before(validDeadline) {
				break
			}
			rebindCtx, rebindCancel := context.WithDeadline(ctx, minTime(validDeadline, now.Add(2*time.Second)))
			nt1, nt2, nv, err := l.r.Rebind(rebindCtx)
			rebindCancel()
			if ctx.Err() != nil {
				return
			}
			if err == nil {
				t1, t2, valid = defaultsFor(LeaseTimes{T1: nt1, T2: nt2, Valid: nv})
				t1Deadline = time.Now().Add(t1)
				t2Deadline = time.Now().Add(t2)
				validDeadline = time.Now().Add(valid)
				rebindOK = true
				break
			}
			slog.Debug("dhcp6: rebind failed, retrying", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryBackoff(t1, t2)):
			}
		}
		if rebindOK {
			continue
		}
		// Phase 4: lease expired. Bail out — caller restarts via fresh Solicit.
		slog.Warn("dhcp6: lease expired, exiting loop")
		return
	}
}

// Wait blocks until Run has returned.
func (l *LeaseLoop) Wait() {
	<-l.done
}

func defaultsFor(in LeaseTimes) (t1, t2, valid time.Duration) {
	valid = in.Valid
	if valid <= 0 {
		valid = 24 * time.Hour
	}
	t1 = in.T1
	if t1 <= 0 {
		t1 = valid / 2
	}
	t2 = in.T2
	if t2 <= 0 {
		t2 = (valid * 4) / 5
	}
	return
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func retryBackoff(t1, t2 time.Duration) time.Duration {
	d := (t2 - t1) / 4
	if d < 100*time.Millisecond {
		d = 100 * time.Millisecond
	}
	if d > 10*time.Second {
		d = 10 * time.Second
	}
	return d
}

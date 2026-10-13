//go:build linux

package renumber

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"
)

// Source emits Prefix events when the upstream prefix changes.
type Source interface {
	// Run blocks until ctx is canceled. Emits prefixes on the returned channel.
	Run(ctx context.Context) <-chan *net.IPNet
	// Stop performs any cleanup (e.g., DHCPv6 Release).
	Stop()
}

// Applier applies a prefix change.
type Applier interface {
	Apply(ctx context.Context, prefix *net.IPNet) error
}

// Service supervises a Source and an Applier with single-flight + debounce.
type Service struct {
	source  Source
	applier Applier

	mu      sync.Mutex
	pending *net.IPNet
	current *net.IPNet
	running bool
	// drainWG tracks in-flight drain goroutines so Run() can wait for them
	// before calling Source.Stop(). This prevents Source.Stop's side
	// effects (e.g. DHCPv6 Release) from racing an in-flight applier.Apply.
	drainWG sync.WaitGroup

	debounce time.Duration
	// retryBackoff is the first wait before retrying a failed Apply; it
	// doubles per consecutive failure up to maxRetryBackoff.
	retryBackoff time.Duration
}

const (
	defaultRetryBackoff = 5 * time.Second
	maxRetryBackoff     = 60 * time.Second
)

// NewService constructs a service. debounce ≤ 0 → 2s default.
func NewService(src Source, app Applier, debounce time.Duration) *Service {
	if debounce <= 0 {
		debounce = 2 * time.Second
	}
	return &Service{source: src, applier: app, debounce: debounce, retryBackoff: defaultRetryBackoff}
}

// Run blocks until ctx is canceled.
func (s *Service) Run(ctx context.Context) {
	events := s.source.Run(ctx)
	defer func() {
		// Wait for in-flight drains before stopping the source so
		// Source.Stop (e.g. DHCPv6 Release) cannot race a half-finished
		// applier.Apply.
		s.drainWG.Wait()
		s.source.Stop()
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case p, ok := <-events:
			if !ok {
				return
			}
			s.enqueue(ctx, p)
		}
	}
}

func (s *Service) enqueue(ctx context.Context, p *net.IPNet) {
	s.mu.Lock()
	if s.current != nil && cidrEqual(s.current, p) {
		s.mu.Unlock()
		return
	}
	s.pending = p
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.drainWG.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.drainWG.Done()
		s.drain(ctx)
	}()
}

func (s *Service) drain(ctx context.Context) {
	backoff := s.retryBackoff
	for {
		// Atomically observe `pending` AND surrender `running` if there's
		// nothing to do. An enqueue arriving after this lock release sees
		// `running == false` and starts a new drain. There is no window
		// where pending could be set with no goroutine to read it.
		s.mu.Lock()
		next := s.pending
		s.pending = nil
		if next == nil {
			s.running = false
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()

		// Debounce: coalesce any further events that arrive in this window.
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
			return
		case <-time.After(s.debounce):
		}
		s.mu.Lock()
		if s.pending != nil {
			next = s.pending
			s.pending = nil
		}
		// Mark next as current while it is applied: a duplicate is
		// dropped, while any other prefix (including the previous one)
		// queues behind it.
		s.current = next
		s.mu.Unlock()

		if err := s.applier.Apply(ctx, next); err != nil {
			slog.Error("renumber: apply failed", "prefix", next, "err", err, "retry_in", backoff)
			// The host may now be half-way to next, so nothing is current:
			// a revert to the old prefix must be applied, not deduplicated.
			s.mu.Lock()
			s.current = nil
			s.mu.Unlock()
			// Sources do not re-emit an unchanged prefix, so retry it here
			// after a backoff. A newer prefix enqueued meanwhile wins.
			select {
			case <-ctx.Done():
				s.mu.Lock()
				s.running = false
				s.mu.Unlock()
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, maxRetryBackoff)
			s.mu.Lock()
			if s.pending == nil {
				s.pending = next
			}
			s.mu.Unlock()
			continue
		}
		backoff = s.retryBackoff
	}
}

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
	// before calling Source.Stop() (F15). This prevents Source.Stop's side
	// effects (e.g. DHCPv6 Release) from racing an in-flight applier.Apply.
	drainWG sync.WaitGroup

	debounce time.Duration
}

// NewService constructs a service. debounce ≤ 0 → 2s default.
func NewService(src Source, app Applier, debounce time.Duration) *Service {
	if debounce <= 0 {
		debounce = 2 * time.Second
	}
	return &Service{source: src, applier: app, debounce: debounce}
}

// Run blocks until ctx is canceled.
func (s *Service) Run(ctx context.Context) {
	events := s.source.Run(ctx)
	defer func() {
		// Wait for in-flight drains before stopping the source so
		// Source.Stop (e.g. DHCPv6 Release) cannot race a half-finished
		// applier.Apply (F15).
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
	for {
		// Atomically observe `pending` AND surrender `running` if there's
		// nothing to do. An enqueue arriving after this lock release sees
		// `running == false` and starts a new drain. There is no longer a
		// window where pending could be set with no goroutine to read it (F14).
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
		s.mu.Unlock()

		if err := s.applier.Apply(ctx, next); err != nil {
			slog.Error("renumber: apply failed", "prefix", next, "err", err)
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
			return
		}
		s.mu.Lock()
		s.current = next
		s.mu.Unlock()
	}
}

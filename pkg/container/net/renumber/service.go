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
	defer s.source.Stop()

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
	s.mu.Unlock()
	go s.drain(ctx)
}

func (s *Service) drain(ctx context.Context) {
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	for {
		s.mu.Lock()
		next := s.pending
		s.pending = nil
		s.mu.Unlock()
		if next == nil {
			return
		}

		// Debounce: coalesce any further events that arrive in this window.
		select {
		case <-ctx.Done():
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
			return
		}
		s.mu.Lock()
		s.current = next
		s.mu.Unlock()
	}
}

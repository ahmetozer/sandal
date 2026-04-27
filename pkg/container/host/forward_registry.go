//go:build linux

package host

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/forward"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
)

// forwardSession owns one container's port-forward resources: the
// listener stop func, the relay context's CancelFunc, and the netns
// dialer pool. close releases all three exactly once.
type forwardSession struct {
	stop   func()
	cancel context.CancelFunc
	dialer *forward.NetnsDialer
}

func (s *forwardSession) close() {
	if s == nil {
		return
	}
	if s.stop != nil {
		s.stop()
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.dialer != nil {
		s.dialer.Close()
	}
}

// forwardRegistry indexes active forward sessions by container name.
// Safe for concurrent use. Lives in pkg/container/host so both crun.go
// (which inserts) and pkg/daemon (which rehydrates and tears down on
// shutdown) can reach it without an import cycle.
type forwardRegistry struct {
	mu       sync.Mutex
	sessions map[string]*forwardSession
}

// Forwards is the process-wide singleton. Only the daemon process and
// daemon-spawned crun() invocations populate it; for foreground/non-
// daemon flows the registry is unused.
var Forwards = &forwardRegistry{sessions: map[string]*forwardSession{}}

// Add registers a session for name, stopping any existing session in
// that slot first. This handles container-restart races where a new
// session is built before the wait goroutine for the old one fires.
func (r *forwardRegistry) Add(name string, s *forwardSession) {
	r.mu.Lock()
	old := r.sessions[name]
	r.sessions[name] = s
	r.mu.Unlock()
	old.close()
}

// Has reports whether a session is currently registered for name.
func (r *forwardRegistry) Has(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.sessions[name]
	return ok
}

// Stop tears down the session for name. No-op if absent.
func (r *forwardRegistry) Stop(name string) {
	r.mu.Lock()
	s := r.sessions[name]
	delete(r.sessions, name)
	r.mu.Unlock()
	s.close()
}

// StopAll closes every active session. Called from the daemon shutdown
// path before containers are signalled.
func (r *forwardRegistry) StopAll() {
	r.mu.Lock()
	all := r.sessions
	r.sessions = map[string]*forwardSession{}
	r.mu.Unlock()
	for _, s := range all {
		s.close()
	}
}

// startForward builds the dialer + listener triple for c.Ports and
// returns a session ready for registration. The caller decides whether
// to register it (daemon background path) or close it on function
// return (foreground path keeps the existing defer model via close).
func startForward(c *config.Config) (*forwardSession, error) {
	if len(c.Ports) == 0 {
		return nil, nil
	}
	forward.AssignIDs(c.Ports)

	dialer, err := forward.StartNetnsDialer(c.ContPid, c.Ports)
	if err != nil {
		return nil, fmt.Errorf("netns dialer: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var (
		stop func()
		sErr error
	)
	if os.Getenv("SANDAL_VM") != "" {
		stop, sErr = forward.StartVsock(ctx, c.Name, c.Ports, dialer)
	} else {
		stop, sErr = forward.Start(ctx, c.Name, c.Ports, dialer)
	}
	if sErr != nil {
		cancel()
		dialer.Close()
		return nil, fmt.Errorf("listeners: %w", sErr)
	}
	return &forwardSession{stop: stop, cancel: cancel, dialer: dialer}, nil
}

// RehydrateForward rebuilds port-forward listeners + dialer for an
// already-running container, using its persisted config. Called from
// the daemon start path so a daemon restart doesn't leave running
// containers without forwarding.
func RehydrateForward(c *config.Config) error {
	// VM containers reach the in-VM relay via vsock; the listening side
	// runs on the host with VsockTransport, not via setns into the
	// container. The current code path here only covers native
	// containers; VM forwarding is set up elsewhere on the host side.
	if c.VM != "" {
		return nil
	}
	s, err := startForward(c)
	if err != nil {
		return err
	}
	if s == nil {
		return nil
	}
	Forwards.Add(c.Name, s)
	return nil
}

// RehydrateAllForwards walks every container and rebuilds forwarding
// for those that are still running and have ports configured. Skips
// containers already registered (the startup-restore path may have
// just installed them). Logs and continues on per-container errors.
func RehydrateAllForwards() {
	conts, err := controller.Containers()
	if err != nil {
		slog.Warn("forward: rehydrate list", "err", err)
		return
	}
	for _, c := range conts {
		if len(c.Ports) == 0 {
			continue
		}
		if Forwards.Has(c.Name) {
			continue
		}
		checkPid := c.ContPid
		if c.VM != "" {
			checkPid = c.HostPid
		}
		if alive, _ := crt.IsPidRunning(checkPid); !alive {
			continue
		}
		if err := RehydrateForward(c); err != nil {
			slog.Warn("forward: rehydrate", "name", c.Name, "err", err)
			continue
		}
		slog.Info("forward: rehydrated", "name", c.Name, "ports", len(c.Ports))
	}
}

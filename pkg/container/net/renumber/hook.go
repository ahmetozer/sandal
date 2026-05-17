//go:build linux

package renumber

import (
	"net"
	"sync"

	"github.com/ahmetozer/sandal/pkg/container/config"
	cnet "github.com/ahmetozer/sandal/pkg/container/net"
)

var (
	hookMu    sync.Mutex
	activeNDP *NDPProxy
	applyMu   sync.Mutex
)

// SetActiveNDPProxy registers the currently-active NDP proxy (called by the
// daemon at service startup). Safe to call multiple times; no-op when nil.
func SetActiveNDPProxy(p *NDPProxy) {
	hookMu.Lock()
	defer hookMu.Unlock()
	activeNDP = p
}

// WithApplyLock runs f while holding the apply-lock; the lock serializes
// renumber.Apply against per-container start/stop hook callbacks so they
// cannot race against NDP proxy reconcile.
func WithApplyLock(f func()) {
	applyMu.Lock()
	defer applyMu.Unlock()
	f()
}

// ReconcileProxyForRunning computes the desired NDP-proxy set from the
// currently-running containers and reconciles the kernel proxy table. Safe to
// call repeatedly from the daemon's health-check tick.
//
// isAlive (optional) tells the reconciler whether a container's kernel PID is
// still alive. Pass nil to skip the kernel-PID check (then the config Status
// field is the only liveness signal). Callers in the daemon should supply a
// callback wrapping crt.IsPidRunning so stale "running" statuses (e.g. after
// a daemon crash) don't keep proxy entries pinned.
func ReconcileProxyForRunning(conts []*config.Config, isAlive func(*config.Config) bool) {
	hookMu.Lock()
	p := activeNDP
	hookMu.Unlock()
	if p == nil {
		return
	}
	WithApplyLock(func() {
		var desired []net.IP
		for _, c := range conts {
			if c == nil || !IsRunning(c) {
				continue
			}
			if isAlive != nil && !isAlive(c) {
				continue
			}
			// VM containers manage their own IPv6 inside the guest.
			if c.VM != "" {
				continue
			}
			if c.NS.Get("net").IsHost {
				continue
			}
			links, err := cnet.ToLinks(&c.Net)
			if err != nil {
				continue
			}
			for _, l := range *links {
				if !l.Dynamic {
					continue
				}
				// In-container DHCPv6 owns this link's IPv6.
				if l.DHCPv6 {
					continue
				}
				for _, a := range l.Addr {
					if a.IP.To4() != nil {
						continue
					}
					if a.IP.IsLinkLocalUnicast() || isULA(a.IP) {
						continue
					}
					desired = append(desired, a.IP)
				}
			}
		}
		p.Reconcile(desired)
	})
}

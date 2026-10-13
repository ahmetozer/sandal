//go:build linux

package renumber

import (
	"log/slog"
	"net"
	"sync"

	"github.com/ahmetozer/sandal/pkg/container/config"
	cnet "github.com/ahmetozer/sandal/pkg/container/net"
)

var (
	hookMu    sync.Mutex
	activeNDP *NDPProxy
	applyMu   sync.Mutex

	// State of the last successful Apply; nil/empty until then. Guarded by
	// applyMu.
	activePrefix    *net.IPNet
	activeMask      net.IPMask
	activeBridgeIPs []net.IP

	// reconcileErrs holds the last reconcile error per container name, so
	// a failure repeating every health-check tick is logged at Warn once.
	// Guarded by applyMu.
	reconcileErrs = map[string]string{}
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

// desiredProxy lists every global that must be answered for on the
// upstream: sandal0's own globals (with no SLAAC on the uplink they are the
// host's only global source addresses) and the globals of eligible
// containers and VMs on sandal0. Only addresses inside prefix are returned,
// so a VM still holding a pre-rotation address, or a static literal from
// another prefix, is not advertised.
func desiredProxy(conts []*config.Config, isAlive func(*config.Config) bool, prefix *net.IPNet, bridgeIPs []net.IP) []net.IP {
	if prefix == nil {
		return nil
	}
	desired := append([]net.IP(nil), bridgeIPs...)
	for _, c := range conts {
		if !eligible(c, isAlive) {
			continue
		}
		links, err := cnet.ToLinks(&c.Net)
		if err != nil {
			continue
		}
		for _, l := range *links {
			if !l.Dynamic || l.DHCPv6 || l.Master != cnet.DefaultBridgeInterface {
				continue
			}
			for _, a := range l.Addr {
				if isGlobal(a.IP) && prefix.Contains(a.IP) {
					desired = append(desired, a.IP)
				}
			}
		}
	}
	return desired
}

// ReconcileProxyForRunning computes the desired NDP-proxy set from the
// currently-running containers and VMs and reconciles the kernel proxy table.
// VMs are included; their addresses are fixed at boot. Safe to call repeatedly
// from the daemon's health-check tick.
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
		if activePrefix == nil {
			return
		}
		p.Reconcile(desiredProxy(conts, isAlive, activePrefix, activeBridgeIPs))
	})
}

// ReconcileContainersForRunning re-checks running containers against the
// active prefix. Called from the daemon's health-check tick so containers
// started after the last Apply (boot ordering, crash recovery, sandal run
// before the prefix was known) converge within one tick. No-op until the
// first Apply.
func ReconcileContainersForRunning(conts []*config.Config, isAlive func(*config.Config) bool) {
	WithApplyLock(func() {
		if activePrefix == nil {
			return
		}
		seen := make(map[string]bool, len(conts))
		for _, c := range conts {
			if !eligible(c, isAlive) || c.VM != "" {
				continue
			}
			seen[c.Name] = true
			err := reconcileContainer(c, activePrefix, activeMask)
			if err == nil {
				delete(reconcileErrs, c.Name)
				continue
			}
			if msg := err.Error(); reconcileErrs[c.Name] != msg {
				reconcileErrs[c.Name] = msg
				slog.Warn("renumber: container reconcile failed", "name", c.Name, "err", err)
			} else {
				slog.Debug("renumber: container reconcile failed", "name", c.Name, "err", err)
			}
		}
		for name := range reconcileErrs {
			if !seen[name] {
				delete(reconcileErrs, name)
			}
		}
	})
}

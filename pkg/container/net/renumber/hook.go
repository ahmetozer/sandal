//go:build linux

package renumber

import (
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

// OnContainerStart adds proxy entries for every global IPv6 on the container's
// links. No-op when NDP proxy is not active.
func OnContainerStart(c *config.Config) {
	hookMu.Lock()
	p := activeNDP
	hookMu.Unlock()
	if p == nil || c == nil {
		return
	}
	WithApplyLock(func() {
		links, err := cnet.ToLinks(&c.Net)
		if err != nil {
			return
		}
		for _, l := range *links {
			for _, a := range l.Addr {
				if a.IP.To4() != nil {
					continue
				}
				if a.IP.IsLinkLocalUnicast() || isULA(a.IP) {
					continue
				}
				_ = p.Add(a.IP)
			}
		}
	})
}

// OnContainerStop removes proxy entries for the container's IPs.
func OnContainerStop(c *config.Config) {
	hookMu.Lock()
	p := activeNDP
	hookMu.Unlock()
	if p == nil || c == nil {
		return
	}
	WithApplyLock(func() {
		links, err := cnet.ToLinks(&c.Net)
		if err != nil {
			return
		}
		for _, l := range *links {
			for _, a := range l.Addr {
				if a.IP.To4() != nil {
					continue
				}
				_ = p.Remove(a.IP)
			}
		}
	})
}

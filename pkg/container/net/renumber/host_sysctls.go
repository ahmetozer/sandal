//go:build linux

package renumber

import (
	"log/slog"
	"sync"

	"github.com/ahmetozer/sandal/pkg/lib/sysctl"
	"github.com/vishvananda/netlink"
)

// HostSysctlsConfig captures the per-interface sysctls required for dynamic
// IPv6 to function.
type HostSysctlsConfig struct {
	Upstream string // upstream interface name (e.g. eth0)
	Bridge   string // bridge interface name (e.g. sandal0)
	Mode     string // "ndp-proxy" | "pd" | "off"
}

// ApplyHostSysctls configures the kernel sysctls the dynamic IPv6 service
// depends on. Called via EnsureHostSysctls at service start and on every
// health-check tick. Per-sysctl failures are logged once and not fatal so a
// sandboxed daemon can still proceed with a degraded subset; a key whose
// write failed is not retried until its value changes.
//
// Always set when upstream is known:
//
//	net.ipv6.conf.all.forwarding         = 1
//	net.ipv6.conf.<upstream>.accept_ra   = 2   accept RA even with forwarding on
//	net.ipv6.conf.<upstream>.forwarding  = 1
//	net.ipv6.conf.<bridge>.forwarding    = 1
//
// Conditional on mode == "ndp-proxy":
//
//	net.ipv6.conf.<upstream>.proxy_ndp   = 1
func ApplyHostSysctls(cfg HostSysctlsConfig) {
	hostSysctlsMu.Lock()
	defer hostSysctlsMu.Unlock()
	for _, kv := range hostSysctls(cfg) {
		// An unreadable key reads as "": it is attempted once, then
		// skipped like any other value whose write failed.
		current, _ := sysctl.Read(kv.Key)
		failedAt, failed := sysctlFailures[kv.Key]
		switch sysctlActionFor(kv.Value, current, failedAt, failed) {
		case sysctlInSync:
			delete(sysctlFailures, kv.Key)
		case sysctlSkip:
			slog.Debug("renumber: sysctl still not settable", "key", kv.Key, "want", kv.Value, "current", current)
		case sysctlWrite:
			if err := sysctl.Write(kv.Key, kv.Value); err != nil {
				sysctlFailures[kv.Key] = current
				slog.Warn("renumber: failed to set sysctl",
					"key", kv.Key, "want", kv.Value, "current", current, "err", err)
				continue
			}
			delete(sysctlFailures, kv.Key)
			slog.Info("renumber: sysctl set", "key", kv.Key, "from", current, "to", kv.Value)
		}
	}
}

type sysctlAction int

const (
	sysctlInSync sysctlAction = iota // already at the wanted value
	sysctlSkip                       // write failed before at this value
	sysctlWrite                      // drifted: write it
)

// sysctlActionFor decides what to do with a key whose wanted value is want
// and current value is current. failedAt is the value the key held when a
// write last failed (failed reports whether one did); retrying only after
// the value changes keeps a read-only /proc/sys from logging every tick.
func sysctlActionFor(want, current, failedAt string, failed bool) sysctlAction {
	if current == want {
		return sysctlInSync
	}
	if failed && failedAt == current {
		return sysctlSkip
	}
	return sysctlWrite
}

// HostSysctlEntry is one configured sysctl key/value pair. Exposed (along
// with hostSysctls) so tests can assert the planned configuration without
// touching /proc/sys.
type HostSysctlEntry struct {
	Key, Value string
}

// hostSysctls returns the deterministic list of sysctls ApplyHostSysctls
// would write for cfg. Empty Upstream or Bridge yields an empty plan.
func hostSysctls(cfg HostSysctlsConfig) []HostSysctlEntry {
	if cfg.Upstream == "" || cfg.Bridge == "" {
		return nil
	}
	plan := []HostSysctlEntry{
		{"net.ipv6.conf.all.forwarding", "1"},
		{"net.ipv6.conf." + cfg.Upstream + ".accept_ra", "2"},
		{"net.ipv6.conf." + cfg.Upstream + ".forwarding", "1"},
		{"net.ipv6.conf." + cfg.Bridge + ".forwarding", "1"},
	}
	if cfg.Mode == "ndp-proxy" {
		plan = append(plan, HostSysctlEntry{
			Key:   "net.ipv6.conf." + cfg.Upstream + ".proxy_ndp",
			Value: "1",
		})
	}
	return plan
}

var (
	hostSysctlsMu     sync.Mutex
	activeHostSysctls HostSysctlsConfig
	// sysctlFailures maps a key whose write failed to the value it held
	// then. Guarded by hostSysctlsMu.
	sysctlFailures = map[string]string{}
)

// SetActiveHostSysctls records the configuration EnsureHostSysctls
// re-applies (called by the daemon once the upstream is known).
func SetActiveHostSysctls(cfg HostSysctlsConfig) {
	hostSysctlsMu.Lock()
	defer hostSysctlsMu.Unlock()
	activeHostSysctls = cfg
}

// EnsureHostSysctls re-applies the active sysctls. Safe on every health-check
// tick: silent while the upstream interface or the bridge does not exist,
// and ApplyHostSysctls only writes (and logs) on drift. Covers an uplink
// created after the daemon starts, or recreated, which resets its
// per-interface sysctls to the defaults.
func EnsureHostSysctls() {
	hostSysctlsMu.Lock()
	cfg := activeHostSysctls
	hostSysctlsMu.Unlock()
	if cfg.Upstream == "" || cfg.Bridge == "" {
		return
	}
	for _, name := range []string{cfg.Upstream, cfg.Bridge} {
		if _, err := netlink.LinkByName(name); err != nil {
			return
		}
	}
	ApplyHostSysctls(cfg)
}

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
// health-check tick. Per-sysctl failures are logged
// and not fatal so a sandboxed daemon can still proceed with a degraded
// subset.
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
	for _, kv := range hostSysctls(cfg) {
		if _, err := sysctl.Ensure(kv.Key, kv.Value); err != nil {
			slog.Warn("renumber: failed to set sysctl",
				"key", kv.Key, "want", kv.Value, "err", err)
		}
	}
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
)

// SetActiveHostSysctls records the configuration EnsureHostSysctls
// re-applies (called by the daemon once the upstream is known).
func SetActiveHostSysctls(cfg HostSysctlsConfig) {
	hostSysctlsMu.Lock()
	defer hostSysctlsMu.Unlock()
	activeHostSysctls = cfg
}

// EnsureHostSysctls re-applies the active sysctls. Safe on every health-check
// tick: silent while the upstream interface does not exist, and
// sysctl.Ensure only writes (and logs) on drift. Covers an uplink created
// after the daemon starts, or recreated, which resets its per-interface
// sysctls to the defaults.
func EnsureHostSysctls() {
	hostSysctlsMu.Lock()
	cfg := activeHostSysctls
	hostSysctlsMu.Unlock()
	if cfg.Upstream == "" {
		return
	}
	if _, err := netlink.LinkByName(cfg.Upstream); err != nil {
		return
	}
	ApplyHostSysctls(cfg)
}

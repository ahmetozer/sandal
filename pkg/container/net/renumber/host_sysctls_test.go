//go:build linux

package renumber

import (
	"reflect"
	"testing"
)

func TestHostSysctlsPlanNDPProxy(t *testing.T) {
	got := hostSysctls(HostSysctlsConfig{
		Upstream: "eth0",
		Bridge:   "sandal0",
		Mode:     "ndp-proxy",
	})
	want := []HostSysctlEntry{
		{"net.ipv6.conf.all.forwarding", "1"},
		{"net.ipv6.conf.eth0.accept_ra", "2"},
		{"net.ipv6.conf.eth0.forwarding", "1"},
		{"net.ipv6.conf.sandal0.forwarding", "1"},
		{"net.ipv6.conf.eth0.proxy_ndp", "1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ndp-proxy plan mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestHostSysctlsPlanPDMode(t *testing.T) {
	got := hostSysctls(HostSysctlsConfig{
		Upstream: "eth0",
		Bridge:   "sandal0",
		Mode:     "pd",
	})
	// proxy_ndp is NOT set in PD mode.
	for _, e := range got {
		if e.Key == "net.ipv6.conf.eth0.proxy_ndp" {
			t.Fatalf("PD mode should not set proxy_ndp, got entry %v", e)
		}
	}
	if len(got) != 4 {
		t.Fatalf("PD plan should have 4 entries, got %d: %v", len(got), got)
	}
}

func TestHostSysctlsPlanEmptyUpstreamYieldsNil(t *testing.T) {
	if got := hostSysctls(HostSysctlsConfig{Bridge: "sandal0", Mode: "ndp-proxy"}); got != nil {
		t.Fatalf("empty upstream should yield nil plan, got %v", got)
	}
	if got := hostSysctls(HostSysctlsConfig{Upstream: "eth0", Mode: "ndp-proxy"}); got != nil {
		t.Fatalf("empty bridge should yield nil plan, got %v", got)
	}
}

func TestHostSysctlsPlanOffMode(t *testing.T) {
	// "off" is not "ndp-proxy" so proxy_ndp is skipped; the always-on
	// settings still apply. (Caller in daemon/start.go ordinarily skips
	// the helper entirely when mode=off; the helper itself is permissive.)
	got := hostSysctls(HostSysctlsConfig{
		Upstream: "eth0",
		Bridge:   "sandal0",
		Mode:     "off",
	})
	for _, e := range got {
		if e.Key == "net.ipv6.conf.eth0.proxy_ndp" {
			t.Fatalf("off mode should not set proxy_ndp, got entry %v", e)
		}
	}
}

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

func TestEnsureHostSysctlsNoopWithoutConfigOrLink(t *testing.T) {
	t.Cleanup(func() { SetActiveHostSysctls(HostSysctlsConfig{}) })
	SetActiveHostSysctls(HostSysctlsConfig{})
	EnsureHostSysctls() // no config → no-op
	SetActiveHostSysctls(HostSysctlsConfig{Upstream: "sandaltest-missing0", Bridge: "sandaltest-missing1", Mode: "ndp-proxy"})
	EnsureHostSysctls() // upstream missing → no-op, /proc/sys untouched
}

func TestSysctlAction(t *testing.T) {
	cases := []struct {
		name          string
		want, current string
		failedAt      string
		failed        bool
		wantAction    sysctlAction
	}{
		{"already set", "1", "1", "", false, sysctlInSync},
		{"already set clears a past failure", "1", "1", "0", true, sysctlInSync},
		{"drift is written", "1", "0", "", false, sysctlWrite},
		{"same failed value is skipped", "1", "0", "0", true, sysctlSkip},
		{"value changed since the failure is retried", "2", "1", "0", true, sysctlWrite},
		{"unreadable key failing again is skipped", "1", "", "", true, sysctlSkip},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sysctlActionFor(tc.want, tc.current, tc.failedAt, tc.failed); got != tc.wantAction {
				t.Fatalf("sysctlActionFor = %v, want %v", got, tc.wantAction)
			}
		})
	}
}

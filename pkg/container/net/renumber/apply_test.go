//go:build linux

package renumber

import (
	"bytes"
	"net"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	cnet "github.com/ahmetozer/sandal/pkg/container/net"
	"github.com/vishvananda/netlink"
)

func TestWithIIDPreservesPrefixBits(t *testing.T) {
	_, prefix, _ := net.ParseCIDR("2001:db8:abcd:1234::/64")
	iid := []byte{0, 0, 0, 0, 0, 0, 0, 0x42}
	got := withIID(prefix, iid)
	want := net.ParseIP("2001:db8:abcd:1234::42").To16()
	if !got.Equal(want) {
		t.Errorf("withIID: got %s, want %s", got, want)
	}
}

func TestWithIIDRespectsPrefixLength(t *testing.T) {
	// /48 prefix: top 48 bits come from prefix; lower 80 bits from iid view.
	// withIID still copies prefix.IP fully then overlays IID into 8..16 with
	// the prefix mask applied; for /48, that means bytes 6 and 7 are owned by
	// IID too, but withIID's current contract is "lower 64 bits = iid", and
	// bytes 0..7 stay prefix. Test that.
	_, prefix, _ := net.ParseCIDR("2001:db8:cafe::/48")
	iid := []byte{0, 0, 0, 0, 0, 0, 0, 0x01}
	got := withIID(prefix, iid)
	// Result should be 2001:db8:cafe:0000:0000:0000:0000:0001
	want := net.ParseIP("2001:db8:cafe::1").To16()
	if !got.Equal(want) {
		t.Errorf("withIID /48: got %s, want %s", got, want)
	}
}

func TestBridgeIIDPicksFirstGlobal(t *testing.T) {
	ll := mustParseAddr(t, "fe80::1/64")
	ula := mustParseAddr(t, "fd00::1/64")
	global := mustParseAddr(t, "2001:db8::abcd/64")
	other := mustParseAddr(t, "2001:db8:9::ffff/64")

	got := bridgeIID([]netlink.Addr{ll, ula, global, other})
	want := []byte{0, 0, 0, 0, 0, 0, 0xab, 0xcd}
	if !bytes.Equal(got, want) {
		t.Errorf("bridgeIID: got %x, want %x", got, want)
	}
}

func TestBridgeIIDFallsBackTo1WhenNoGlobal(t *testing.T) {
	ll := mustParseAddr(t, "fe80::1/64")
	ula := mustParseAddr(t, "fd00::1/64")

	got := bridgeIID([]netlink.Addr{ll, ula})
	want := []byte{0, 0, 0, 0, 0, 0, 0, 1}
	if !bytes.Equal(got, want) {
		t.Errorf("bridgeIID fallback: got %x, want %x", got, want)
	}
}

func TestPickGlobalLocal(t *testing.T) {
	ll := cnet.Addr{IP: net.ParseIP("fe80::1"), IPNet: &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}}
	ula := cnet.Addr{IP: net.ParseIP("fd00::1"), IPNet: &net.IPNet{IP: net.ParseIP("fd00::1"), Mask: net.CIDRMask(64, 128)}}
	v4 := cnet.Addr{IP: net.ParseIP("172.16.0.2"), IPNet: &net.IPNet{IP: net.ParseIP("172.16.0.2"), Mask: net.CIDRMask(24, 32)}}
	global := cnet.Addr{IP: net.ParseIP("2001:db8::42"), IPNet: &net.IPNet{IP: net.ParseIP("2001:db8::42"), Mask: net.CIDRMask(64, 128)}}

	if pickGlobalLocal(cnet.Addrs{ll, v4, ula}) != nil {
		t.Error("pickGlobalLocal: expected nil when only LL/ULA/v4 present")
	}
	got := pickGlobalLocal(cnet.Addrs{ll, ula, v4, global})
	if got == nil {
		t.Fatal("pickGlobalLocal: returned nil for set with a global")
	}
	if !got.IP.Equal(global.IP) {
		t.Errorf("pickGlobalLocal: got %s, want %s", got.IP, global.IP)
	}
}

func TestPickIIDLocal(t *testing.T) {
	global := cnet.Addr{IP: net.ParseIP("2001:db8::1234:5678"), IPNet: &net.IPNet{IP: net.ParseIP("2001:db8::1234:5678"), Mask: net.CIDRMask(64, 128)}}
	got := pickIIDLocal(cnet.Addrs{global})
	// Compute the expected IID directly from the IP for correctness:
	ip := global.IP.To16()
	want := make([]byte, 8)
	copy(want, ip[8:16])
	if !bytes.Equal(got, want) {
		t.Errorf("pickIIDLocal: got %x, want %x", got, want)
	}
}

func TestPickIIDLocalReturnsNilWhenNoGlobal(t *testing.T) {
	ll := cnet.Addr{IP: net.ParseIP("fe80::1"), IPNet: &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}}
	ula := cnet.Addr{IP: net.ParseIP("fd00::1"), IPNet: &net.IPNet{IP: net.ParseIP("fd00::1"), Mask: net.CIDRMask(64, 128)}}
	if got := pickIIDLocal(cnet.Addrs{ll, ula}); got != nil {
		t.Errorf("pickIIDLocal: expected nil when no global, got %x", got)
	}
}

func TestIsRunning(t *testing.T) {
	cases := []struct {
		name string
		c    *config.Config
		want bool
	}{
		{"nil", nil, false},
		{"pid zero", &config.Config{ContPid: 0, Status: "running"}, false},
		{"exit status", &config.Config{ContPid: 1234, Status: "exit 0"}, false},
		{"err status", &config.Config{ContPid: 1234, Status: "err timeout"}, false},
		{"healthy", &config.Config{ContPid: 1234, Status: "running"}, true},
	}
	for _, tc := range cases {
		if got := IsRunning(tc.c); got != tc.want {
			t.Errorf("%s: IsRunning got %v want %v", tc.name, got, tc.want)
		}
	}
}

func mustParseAddr(t *testing.T, s string) netlink.Addr {
	t.Helper()
	ip, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return netlink.Addr{IPNet: &net.IPNet{IP: ip, Mask: ipnet.Mask}}
}

// TestIPRequestSeesIntraContainerAllocations exercises the invariant that
// `applyLocked` depends on for F4: when two dynamic links inside the SAME
// container need IPs (no preserved IIDs), the second IPRequest must observe
// the first link's freshly-allocated IP via the shared reserved slice. The
// only way this works is if c.Net is updated between the two allocations.
//
// This test asserts the contract: a config c that mirrors apply.go's
// "shadow" slice, with c.Net mutated between IPRequest calls, yields
// distinct IPs.
func TestIPRequestSeesIntraContainerAllocations(t *testing.T) {
	_, prefix, _ := net.ParseCIDR("2001:db8:abcd:1234::/64")

	// One container with the bridge IP at ::1 already occupying the first
	// host bit; subsequent allocations start at ::2.
	c := &config.Config{
		Name: "two-link",
		Net: cnet.Links{
			cnet.Link{Id: "a", Dynamic: true, Addr: cnet.Addrs{}},
			cnet.Link{Id: "b", Dynamic: true, Addr: cnet.Addrs{}},
		},
	}
	reserved := []*config.Config{c}

	// Iteration 0 — allocate for link 0; mutate c.Net so iteration 1
	// observes the new IP via addrInUse (the apply.go fix).
	ip0, err := cnet.IPRequest(&reserved, prefix)
	if err != nil {
		t.Fatalf("IPRequest #1: %v", err)
	}
	links := c.Net.(cnet.Links)
	links[0].Addr = links[0].Addr.ReplaceGlobal(net.IPNet{IP: ip0, Mask: prefix.Mask})
	c.Net = links

	// Iteration 1 — allocate for link 1.
	ip1, err := cnet.IPRequest(&reserved, prefix)
	if err != nil {
		t.Fatalf("IPRequest #2: %v", err)
	}

	if ip0.Equal(ip1) {
		t.Fatalf("intra-container IID collision: link0=%s link1=%s", ip0, ip1)
	}
}

// TestIPRequestCollidesWhenContainerNetNotUpdated demonstrates the failure
// mode: if c.Net is NOT mutated between IPRequest calls (the pre-fix
// behavior of apply.go), the second call returns the same IP as the first.
func TestIPRequestCollidesWhenContainerNetNotUpdated(t *testing.T) {
	_, prefix, _ := net.ParseCIDR("2001:db8:abcd:1234::/64")

	c := &config.Config{
		Name: "buggy",
		Net: cnet.Links{
			cnet.Link{Id: "a", Dynamic: true, Addr: cnet.Addrs{}},
			cnet.Link{Id: "b", Dynamic: true, Addr: cnet.Addrs{}},
		},
	}
	reserved := []*config.Config{c}

	ip0, err := cnet.IPRequest(&reserved, prefix)
	if err != nil {
		t.Fatalf("IPRequest #1: %v", err)
	}
	// Intentionally do NOT update c.Net.
	ip1, err := cnet.IPRequest(&reserved, prefix)
	if err != nil {
		t.Fatalf("IPRequest #2: %v", err)
	}
	if !ip0.Equal(ip1) {
		t.Fatalf("expected collision without c.Net update, got ip0=%s ip1=%s", ip0, ip1)
	}
}

//go:build linux

package net

import (
	"net"
	"os"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/vishvananda/netlink"
)

// End-to-end tests that exercise ResolveHostNet against a real netlink
// interface and walk the result through stringToAddrs + IPRequest.
//
// Requires CAP_NET_ADMIN. Skipped automatically when run as a non-root user
// or when dummy-interface creation fails for any other reason.

const e2eIfName = "sandaltestn0"

// 192.168.99.42 = 0xc0a8:0x632a -> "c0a8:632a"
const e2eHostIPv4 = "192.168.99.42/24"
const e2eIPv4Hextets = "c0a8:632a"

func setupDummyUpstream(t *testing.T) {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("e2e: requires root for netlink dummy creation")
	}
	// Best-effort cleanup of any stale interface from a previous run.
	if old, err := netlink.LinkByName(e2eIfName); err == nil {
		_ = netlink.LinkDel(old)
	}

	link := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: e2eIfName}}
	if err := netlink.LinkAdd(link); err != nil {
		t.Skipf("e2e: cannot create dummy %q (kernel module not loaded?): %v", e2eIfName, err)
	}
	t.Cleanup(func() { _ = netlink.LinkDel(link) })

	addr, err := netlink.ParseAddr(e2eHostIPv4)
	if err != nil {
		t.Fatalf("ParseAddr(%q): %v", e2eHostIPv4, err)
	}
	if err := netlink.AddrAdd(link, addr); err != nil {
		t.Fatalf("AddrAdd: %v", err)
	}
	if err := netlink.LinkSetUp(link); err != nil {
		t.Fatalf("LinkSetUp: %v", err)
	}

	origIf := env.UpstreamInterface
	env.UpstreamInterface = e2eIfName
	t.Cleanup(func() { env.UpstreamInterface = origIf })
}

// TestResolveHostNet_E2E_RealNetlink runs ResolveHostNet end-to-end against a
// real netlink interface. No stub: this exercises upstreamIPv4()'s actual
// kernel lookup path.
func TestResolveHostNet_E2E_RealNetlink(t *testing.T) {
	setupDummyUpstream(t)

	template := "172.16.0.1/24,fd34:0135:0123:0:%v4%::1/120"
	wantResolved := "172.16.0.1/24,fd34:0135:0123:0:" + e2eIPv4Hextets + "::1/120"

	got := ResolveHostNet(template)
	if got != wantResolved {
		t.Fatalf("ResolveHostNet(%q) = %q, want %q", template, got, wantResolved)
	}
}

// TestE2E_BridgeAddrFormation walks the full chain a production daemon takes:
// template -> ResolveHostNet -> stringToAddrs. Verifies the resulting bridge
// addresses are well-formed and that the IPv6 entry encodes the host IPv4 in
// its IID exactly where the design says it should.
func TestE2E_BridgeAddrFormation(t *testing.T) {
	setupDummyUpstream(t)

	resolved := ResolveHostNet("172.16.0.1/24,fd34:0135:0123:0:%v4%::1/120")
	addrs, err := stringToAddrs(resolved)
	if err != nil {
		t.Fatalf("stringToAddrs(%q): %v", resolved, err)
	}
	if len(addrs) != 2 {
		t.Fatalf("expected 2 addrs (v4 + v6), got %d: %#v", len(addrs), addrs)
	}

	// Find the IPv6 entry.
	var v6 *Addr
	for i := range addrs {
		if addrs[i].IP.To4() == nil {
			v6 = &addrs[i]
			break
		}
	}
	if v6 == nil {
		t.Fatalf("no IPv6 address in parsed bridge addrs: %#v", addrs)
	}

	// Verify the address matches the design. Compare via net.IP.Equal so
	// Go's RFC 5952 formatting (which writes :0: for single-hextet zero
	// runs) doesn't trip the assertion.
	want := net.ParseIP("fd34:0135:0123:0:c0a8:632a::1")
	if !v6.IP.Equal(want) {
		t.Errorf("bridge IPv6 = %s, want %s", v6.IP, want)
	}
	if ones, _ := v6.IPNet.Mask.Size(); ones != 120 {
		t.Errorf("bridge mask = /%d, want /120", ones)
	}

	// IID is the lower 64 bits; for fd34:135:123:0:c0a8:632a::1 those are
	// c0a8:632a:0000:0001. The IPv4 hextets must occupy the high half.
	iid := v6.IP.To16()[8:16]
	if iid[0] != 0xc0 || iid[1] != 0xa8 || iid[2] != 0x63 || iid[3] != 0x2a {
		t.Errorf("IID high 32 bits = %x:%x:%x:%x, want c0:a8:63:2a", iid[0], iid[1], iid[2], iid[3])
	}
}

// TestE2E_IPRequestWalksFromResolvedBridge confirms that the resolved bridge
// address feeds IPRequest correctly: first container allocates ::2 (i.e.
// c0a8:632a::2) within the resolved /120, and successive calls walk forward
// without colliding. This is the path sandal-controller takes when sandal run
// creates a new container.
func TestE2E_IPRequestWalksFromResolvedBridge(t *testing.T) {
	setupDummyUpstream(t)

	resolved := ResolveHostNet("172.16.0.1/24,fd34:0135:0123:0:%v4%::1/120")
	addrs, err := stringToAddrs(resolved)
	if err != nil {
		t.Fatalf("stringToAddrs: %v", err)
	}
	var v6net *net.IPNet
	for _, a := range addrs {
		if a.IP.To4() == nil {
			v6net = a.IPNet
			break
		}
	}
	if v6net == nil {
		t.Fatal("no IPv6 net")
	}

	// Seed reserved with a "bridge" entry holding the resolved ULA host IP,
	// so IPRequest skips it and starts allocating from ::2 — exactly the
	// state of the world when sandal0 already exists.
	bridgeIP := net.ParseIP("fd34:0135:0123:0:c0a8:632a::1")
	configs := []*config.Config{{
		Name:   "bridge-placeholder",
		Status: "running",
		Net: Links{Link{
			Id:   "sandal0",
			Addr: Addrs{Addr{IP: bridgeIP, IPNet: &net.IPNet{IP: bridgeIP, Mask: v6net.Mask}}},
		}},
	}}

	wantIPs := []net.IP{
		net.ParseIP("fd34:0135:0123:0:c0a8:632a::2"),
		net.ParseIP("fd34:0135:0123:0:c0a8:632a::3"),
		net.ParseIP("fd34:0135:0123:0:c0a8:632a::4"),
	}
	for i, want := range wantIPs {
		ip, err := IPRequest(&configs, v6net)
		if err != nil {
			t.Fatalf("IPRequest #%d: %v", i, err)
		}
		if !ip.Equal(want) {
			t.Errorf("container #%d = %s, want %s", i, ip, want)
		}
		// Reserve so the next iteration walks past it.
		configs = append(configs, &config.Config{
			Name:   "e2e-" + ip.String(),
			Status: "running",
			Net: Links{Link{
				Id:      "eth0",
				Dynamic: true,
				Addr:    Addrs{Addr{IP: ip, IPNet: &net.IPNet{IP: ip, Mask: v6net.Mask}}},
			}},
		})
	}
}

// TestE2E_PublicMirrorIIDPreservation simulates the renumber stamp: take the
// IID from the resolved ULA, combine with a sample upstream /64 prefix, and
// verify the resulting public address keeps the IPv4 hextets exactly where
// the design promises. Done locally (without importing the renumber package)
// to avoid the cnet->renumber->cnet import cycle.
func TestE2E_PublicMirrorIIDPreservation(t *testing.T) {
	setupDummyUpstream(t)

	resolved := ResolveHostNet("172.16.0.1/24,fd34:0135:0123:0:%v4%::1/120")
	addrs, err := stringToAddrs(resolved)
	if err != nil {
		t.Fatalf("stringToAddrs: %v", err)
	}
	var bridgeV6 net.IP
	for _, a := range addrs {
		if a.IP.To4() == nil {
			bridgeV6 = a.IP.To16()
			break
		}
	}
	if bridgeV6 == nil {
		t.Fatal("no IPv6 bridge addr")
	}
	iid := bridgeV6[8:16]

	// Stamp the IID under a sample ISP /64 — exact same operation
	// renumber.withIID does.
	_, upstream, _ := net.ParseCIDR("2a00:1d35:3b0a:4f00::/64")
	mirror := make(net.IP, 16)
	copy(mirror, upstream.IP.To16())
	copy(mirror[8:16], iid)

	want := net.ParseIP("2a00:1d35:3b0a:4f00:c0a8:632a::1")
	if !mirror.Equal(want) {
		t.Errorf("public mirror = %s, want %s", mirror, want)
	}
}

//go:build linux

package renumber

import (
	"bytes"
	"net"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/namespace"
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

// TestPickIIDLocalFallsBackToULA: when a container is in gap state (created
// before upstream IPv6 arrived), it has only a ULA + link-local. The renumber
// path must extract the ULA's IID so the next-prefix stamp preserves the IID
// structure SANDAL_HOST_NET established (notably the %uv4% IPv4 embedding).
func TestPickIIDLocalFallsBackToULA(t *testing.T) {
	ll := cnet.Addr{IP: net.ParseIP("fe80::1"), IPNet: &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}}
	ula := cnet.Addr{IP: net.ParseIP("fd34:135:123:0:ac10:3::2"), IPNet: &net.IPNet{IP: net.ParseIP("fd34:135:123:0:ac10:3::2"), Mask: net.CIDRMask(120, 128)}}
	got := pickIIDLocal(cnet.Addrs{ll, ula})
	want := []byte{0xac, 0x10, 0x00, 0x03, 0x00, 0x00, 0x00, 0x02}
	if !bytes.Equal(got, want) {
		t.Errorf("pickIIDLocal ULA fallback: got %x, want %x", got, want)
	}
}

// TestPickIIDLocalPrefersGlobalOverULA: once a global exists, it takes
// precedence — the global address's IID is the long-lived identifier and
// MUST stay stable across subsequent renumbers.
func TestPickIIDLocalPrefersGlobalOverULA(t *testing.T) {
	global := cnet.Addr{IP: net.ParseIP("2001:db8::42"), IPNet: &net.IPNet{IP: net.ParseIP("2001:db8::42"), Mask: net.CIDRMask(64, 128)}}
	ula := cnet.Addr{IP: net.ParseIP("fd34:135:123:0:ac10:3::2"), IPNet: &net.IPNet{IP: net.ParseIP("fd34:135:123:0:ac10:3::2"), Mask: net.CIDRMask(120, 128)}}
	got := pickIIDLocal(cnet.Addrs{ula, global})
	want := []byte{0, 0, 0, 0, 0, 0, 0, 0x42}
	if !bytes.Equal(got, want) {
		t.Errorf("pickIIDLocal must prefer global over ULA: got %x, want %x", got, want)
	}
}

func TestPickIIDLocalReturnsNilWhenOnlyLinkLocalOrIPv4(t *testing.T) {
	ll := cnet.Addr{IP: net.ParseIP("fe80::1"), IPNet: &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}}
	v4 := cnet.Addr{IP: net.ParseIP("172.16.0.5"), IPNet: &net.IPNet{IP: net.ParseIP("172.16.0.5"), Mask: net.CIDRMask(24, 32)}}
	if got := pickIIDLocal(cnet.Addrs{ll, v4}); got != nil {
		t.Errorf("pickIIDLocal: expected nil when only LL/IPv4 present, got %x", got)
	}
}

// TestBridgeIIDFallsBackToULA mirrors the pickIIDLocal test but for the
// bridge code path that runs on real netlink.Addr.
func TestBridgeIIDFallsBackToULA(t *testing.T) {
	mustParse := func(s string) netlink.Addr {
		ip, ipnet, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return netlink.Addr{IPNet: &net.IPNet{IP: ip, Mask: ipnet.Mask}}
	}
	addrs := []netlink.Addr{
		mustParse("fe80::1/64"),
		mustParse("fd34:135:123:0:ac10:3::1/120"),
	}
	got := bridgeIID(addrs)
	want := []byte{0xac, 0x10, 0x00, 0x03, 0x00, 0x00, 0x00, 0x01}
	if !bytes.Equal(got, want) {
		t.Errorf("bridgeIID ULA fallback: got %x, want %x", got, want)
	}
}

func TestBridgeIIDPrefersGlobalOverULA(t *testing.T) {
	mustParse := func(s string) netlink.Addr {
		ip, ipnet, err := net.ParseCIDR(s)
		if err != nil {
			t.Fatalf("parse %q: %v", s, err)
		}
		return netlink.Addr{IPNet: &net.IPNet{IP: ip, Mask: ipnet.Mask}}
	}
	addrs := []netlink.Addr{
		mustParse("fd34:135:123:0:ac10:3::1/120"),
		mustParse("2001:db8::99/64"),
	}
	got := bridgeIID(addrs)
	want := []byte{0, 0, 0, 0, 0, 0, 0, 0x99}
	if !bytes.Equal(got, want) {
		t.Errorf("bridgeIID must prefer global: got %x, want %x", got, want)
	}
}

func TestBridgeIIDDefaultWhenEmpty(t *testing.T) {
	got := bridgeIID(nil)
	want := []byte{0, 0, 0, 0, 0, 0, 0, 1}
	if !bytes.Equal(got, want) {
		t.Errorf("bridgeIID empty fallback: got %x, want %x", got, want)
	}
}

// TestPickLookupAddrLocalFallsBackToULA: when a container is in gap state
// (created before upstream IPv6 arrived) it has only a ULA on its link.
// The renumber path needs a way to locate the netns link without a global
// IPv6 — pickLookupAddrLocal returns the ULA so the address-based lookup
// in SwapContainerAddrByOldIP still works.
func TestPickLookupAddrLocalFallsBackToULA(t *testing.T) {
	ll := cnet.Addr{IP: net.ParseIP("fe80::1"), IPNet: &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}}
	ula := cnet.Addr{IP: net.ParseIP("fd34:135:123::5"), IPNet: &net.IPNet{IP: net.ParseIP("fd34:135:123::5"), Mask: net.CIDRMask(64, 128)}}
	v4 := cnet.Addr{IP: net.ParseIP("172.16.0.5"), IPNet: &net.IPNet{IP: net.ParseIP("172.16.0.5"), Mask: net.CIDRMask(24, 32)}}

	got := pickLookupAddrLocal(cnet.Addrs{v4, ll, ula})
	if got == nil {
		t.Fatal("pickLookupAddrLocal returned nil; expected the ULA")
	}
	if !got.IP.Equal(ula.IP) {
		t.Fatalf("pickLookupAddrLocal: got %s, want %s", got.IP, ula.IP)
	}
}

// TestPickLookupAddrLocalPrefersFirstNonLinkLocal documents the contract:
// the function returns the FIRST suitable entry, leaving ordering decisions
// to the caller. (Compare with pickGlobalLocal which is stricter.)
func TestPickLookupAddrLocalPrefersFirstNonLinkLocal(t *testing.T) {
	ll := cnet.Addr{IP: net.ParseIP("fe80::1"), IPNet: &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}}
	global := cnet.Addr{IP: net.ParseIP("2001:db8::1"), IPNet: &net.IPNet{IP: net.ParseIP("2001:db8::1"), Mask: net.CIDRMask(64, 128)}}
	ula := cnet.Addr{IP: net.ParseIP("fd34:135:123::5"), IPNet: &net.IPNet{IP: net.ParseIP("fd34:135:123::5"), Mask: net.CIDRMask(64, 128)}}

	got := pickLookupAddrLocal(cnet.Addrs{ll, global, ula})
	if got == nil || !got.IP.Equal(global.IP) {
		t.Fatalf("expected the global (first non-LL), got %v", got)
	}
}

// TestPickLookupAddrLocalReturnsNilWhenOnlyLinkLocalOrIPv4 confirms there is
// no fallback beyond ULA/global IPv6.
func TestPickLookupAddrLocalReturnsNilWhenOnlyLinkLocalOrIPv4(t *testing.T) {
	ll := cnet.Addr{IP: net.ParseIP("fe80::1"), IPNet: &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}}
	v4 := cnet.Addr{IP: net.ParseIP("172.16.0.5"), IPNet: &net.IPNet{IP: net.ParseIP("172.16.0.5"), Mask: net.CIDRMask(24, 32)}}
	if got := pickLookupAddrLocal(cnet.Addrs{ll, v4}); got != nil {
		t.Errorf("pickLookupAddrLocal: expected nil for IPv4+LL-only set, got %v", got)
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

func mustParseCnetAddr(t *testing.T, s string) cnet.Addr {
	t.Helper()
	ip, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return cnet.Addr{IP: ip, IPNet: n}
}

func TestGlobalAndBridgeMask(t *testing.T) {
	_, prefix, _ := net.ParseCIDR("2001:db8:1:2::/64")
	m64, m128 := net.CIDRMask(64, 128), net.CIDRMask(128, 128)
	pd, ndp := &DefaultApplier{}, &DefaultApplier{HostRoute: true}
	if pd.globalMask(prefix).String() != m64.String() || pd.bridgeMask(m64).String() != m64.String() {
		t.Error("pd mode must keep the prefix mask")
	}
	if ndp.globalMask(prefix).String() != m128.String() || ndp.bridgeMask(m64).String() != m128.String() {
		t.Error("ndp-proxy mode must use /128")
	}
}

func TestEligible(t *testing.T) {
	running := func() *config.Config { return &config.Config{ContPid: 10, Status: "running"} }
	vm := running()
	vm.VM = "kvm"
	hostNet := running()
	hostNet.NS = namespace.Namespaces{"net": {IsHost: true}}
	cases := []struct {
		name    string
		c       *config.Config
		isAlive func(*config.Config) bool
		want    bool
	}{
		{"nil", nil, nil, false},
		{"running", running(), nil, true},
		{"vm is eligible for proxying", vm, nil, true},
		{"host net", hostNet, nil, false},
		{"dead pid", running(), func(*config.Config) bool { return false }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := eligible(tc.c, tc.isAlive); got != tc.want {
				t.Fatalf("eligible = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWantGlobals(t *testing.T) {
	_, prefix, _ := net.ParseCIDR("2001:db8:3:4::/64")
	m128 := net.CIDRMask(128, 128)
	l := cnet.Link{Dynamic: true, Master: cnet.DefaultBridgeInterface, DynamicAddr: []string{"%uv6%:192:0:2:23"}}
	want, ok := wantGlobals(l, prefix, m128)
	if !ok || len(want) != 1 || want[0].String() != "2001:db8:3:4:192:0:2:23/128" {
		t.Fatalf("wantGlobals = %v, %v", want, ok)
	}
	literalOnly := cnet.Link{Dynamic: true, Master: cnet.DefaultBridgeInterface, Addr: cnet.Addrs{mustParseCnetAddr(t, "2001:db8:1:2::23/128")}}
	cases := []struct {
		name   string
		l      cnet.Link
		prefix *net.IPNet
	}{
		{"dynamic=false", cnet.Link{Dynamic: false, Master: l.Master, DynamicAddr: l.DynamicAddr}, prefix},
		{"dhcp6", cnet.Link{Dynamic: true, DHCPv6: true, Master: l.Master, DynamicAddr: l.DynamicAddr}, prefix},
		{"no prefix yet", l, nil},
		{"other bridge", cnet.Link{Dynamic: true, Master: "br-other", DynamicAddr: l.DynamicAddr}, prefix},
		{"literal global is static", literalOnly, prefix},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := wantGlobals(tc.l, tc.prefix, m128); ok {
				t.Fatal("link must not be managed")
			}
		})
	}
}

func TestOwnedGlobalsIgnoresStaticLiteral(t *testing.T) {
	l := cnet.Link{Addr: cnet.Addrs{
		mustParseCnetAddr(t, "192.0.2.23/24"),
		mustParseCnetAddr(t, "2001:db8:1:2:192:0:2:23/128"),
		mustParseCnetAddr(t, "2001:db8:9::7/64"),
		mustParseCnetAddr(t, "fd00:db8::23/64"),
	}}
	want := []net.IPNet{{IP: net.ParseIP("2001:db8:3:4:192:0:2:23"), Mask: net.CIDRMask(128, 128)}}
	got := ownedGlobals(l, want)
	if len(got) != 1 || !got[0].IP.Equal(net.ParseIP("2001:db8:1:2:192:0:2:23")) {
		t.Fatalf("ownedGlobals = %v, want only the IID-matching global", got)
	}
	if sameAddrs(got, want) {
		t.Error("old prefix must differ from new")
	}
	if !sameAddrs(want, want) {
		t.Error("identical sets must compare equal")
	}
}

func TestLookupAddrsSkipsLinkLocal(t *testing.T) {
	got := lookupAddrs(cnet.Addrs{mustParseCnetAddr(t, "fe80::1/64"), mustParseCnetAddr(t, "192.0.2.12/24")})
	if len(got) != 1 || !got[0].Equal(net.ParseIP("192.0.2.12")) {
		t.Fatalf("lookupAddrs = %v, want [192.0.2.12]", got)
	}
}

//go:build linux

package renumber

import (
	"bytes"
	"errors"
	"net"
	"strings"
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

// TestBridgeIIDFallsBackToULA checks that bridgeIID uses the ULA IID when the
// bridge has no global address, using real netlink.Addr values.
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
	// Host-side VM configs carry the KVM process in HostPid; ContPid is
	// never set.
	vm := &config.Config{HostPid: 20, Status: "running", VM: "kvm"}
	vmNoPid := &config.Config{Status: "running", VM: "kvm"}
	hostNet := running()
	hostNet.NS = namespace.Namespaces{"net": {IsHost: true}}
	joinedNet := running()
	joinedNet.NS = namespace.Namespaces{"net": {IsUserDefined: true}}
	exited := running()
	exited.Status = "exit 0"
	cases := []struct {
		name    string
		c       *config.Config
		isAlive func(*config.Config) bool
		want    bool
	}{
		{"nil", nil, nil, false},
		{"running", running(), nil, true},
		{"vm with HostPid only", vm, nil, true},
		{"vm without HostPid", vmNoPid, nil, false},
		{"exited", exited, nil, false},
		{"host net", hostNet, nil, false},
		{"user-defined net", joinedNet, nil, false},
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

func stubSetContainer(t *testing.T) *int {
	t.Helper()
	calls := 0
	orig := setContainerFn
	t.Cleanup(func() { setContainerFn = orig })
	setContainerFn = func(*config.Config) error { calls++; return nil }
	return &calls
}

// ContPid 0 → the netns step is skipped; only planning + persist run.
func TestReconcileContainerFollowsPrefixAndPersistsOnce(t *testing.T) {
	calls := stubSetContainer(t)
	c := &config.Config{Name: "web", Net: cnet.Links{{
		Id: "veth0", Dynamic: true, Master: cnet.DefaultBridgeInterface,
		DynamicAddr: []string{"%uv6%:192:0:2:23"},
		Addr: cnet.Addrs{
			mustParseCnetAddr(t, "192.0.2.23/24"),
			mustParseCnetAddr(t, "2001:db8:1:2:192:0:2:23/128"),
			mustParseCnetAddr(t, "2001:db8:9::7/64"), // static literal
			mustParseCnetAddr(t, "fd00:db8::23/64"),
		},
	}}}
	_, prefix, _ := net.ParseCIDR("2001:db8:3:4::/64")
	m128 := net.CIDRMask(128, 128)

	if err := reconcileContainer(c, prefix, m128); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("persist calls = %d, want 1", *calls)
	}
	links, _ := cnet.ToLinks(&c.Net)
	var ips []string
	for _, a := range (*links)[0].Addr {
		ips = append(ips, (&net.IPNet{IP: a.IP, Mask: a.IPNet.Mask}).String())
	}
	got := strings.Join(ips, ",")
	if !strings.Contains(got, "2001:db8:3:4:192:0:2:23/128") || strings.Contains(got, "2001:db8:1:2:192:0:2:23") {
		t.Fatalf("dynamic global not renumbered: %s", got)
	}
	if !strings.Contains(got, "2001:db8:9::7") {
		t.Fatalf("static literal global must be kept: %s", got)
	}

	if err := reconcileContainer(c, prefix, m128); err != nil {
		t.Fatal(err)
	}
	if *calls != 1 {
		t.Fatalf("steady state must not persist again, calls = %d", *calls)
	}
}

// A link whose netns update fails must not stop the remaining links from
// being renumbered and persisted.
func TestReconcileContainerContinuesPastFailingLink(t *testing.T) {
	calls := stubSetContainer(t)
	orig := setContainerGlobalsFn
	t.Cleanup(func() { setContainerGlobalsFn = orig })
	var tried []string
	setContainerGlobalsFn = func(_ int, _ []net.IP, ifname string, _ []net.IPNet) error {
		tried = append(tried, ifname)
		if ifname == "eth0" {
			return errors.New("netns boom")
		}
		return nil
	}
	link := func(name, v4, global string, dyn string) cnet.Link {
		return cnet.Link{
			Id: "veth-" + name, Name: name, Dynamic: true, Master: cnet.DefaultBridgeInterface,
			DynamicAddr: []string{dyn},
			Addr:        cnet.Addrs{mustParseCnetAddr(t, v4), mustParseCnetAddr(t, global)},
		}
	}
	c := &config.Config{Name: "web", ContPid: 4194304, Net: cnet.Links{
		link("eth0", "192.0.2.23/24", "2001:db8:1:2:192:0:2:23/128", "%uv6%:192:0:2:23"),
		link("eth1", "198.51.100.23/24", "2001:db8:1:2:198:51:100:23/128", "%uv6%:198:51:100:23"),
	}}
	_, prefix, _ := net.ParseCIDR("2001:db8:3:4::/64")

	err := reconcileContainer(c, prefix, net.CIDRMask(128, 128))
	if err == nil || !strings.Contains(err.Error(), "eth0") {
		t.Fatalf("reconcileContainer err = %v, want the eth0 failure", err)
	}
	if strings.Join(tried, ",") != "eth0,eth1" {
		t.Fatalf("netns updates tried = %v, want both links", tried)
	}
	if *calls != 1 {
		t.Fatalf("persist calls = %d, want 1", *calls)
	}
	links, _ := cnet.ToLinks(&c.Net)
	has := func(l cnet.Link, ip string) bool {
		for _, a := range l.Addr {
			if a.IP.Equal(net.ParseIP(ip)) {
				return true
			}
		}
		return false
	}
	if !has((*links)[1], "2001:db8:3:4:198:51:100:23") {
		t.Fatalf("second link not renumbered: %v", (*links)[1].Addr)
	}
	if !has((*links)[0], "2001:db8:1:2:192:0:2:23") {
		t.Fatalf("failed link must keep its old global: %v", (*links)[0].Addr)
	}
}

func TestReconcileContainerLeavesOptedOutLinkAlone(t *testing.T) {
	calls := stubSetContainer(t)
	c := &config.Config{Name: "db", Net: cnet.Links{{
		Id: "veth1", Dynamic: true, Master: cnet.DefaultBridgeInterface,
		Addr: cnet.Addrs{mustParseCnetAddr(t, "192.0.2.12/24"), mustParseCnetAddr(t, "2001:db8:1:2::12/128")},
	}}}
	_, prefix, _ := net.ParseCIDR("2001:db8:3:4::/64")
	if err := reconcileContainer(c, prefix, net.CIDRMask(128, 128)); err != nil {
		t.Fatal(err)
	}
	if *calls != 0 {
		t.Fatalf("link without dynamic addresses touched: persist=%d", *calls)
	}
}

func TestDesiredProxy(t *testing.T) {
	_, prefix, _ := net.ParseCIDR("2001:db8:3:4::/64")
	link := func(addrs ...string) cnet.Links {
		l := cnet.Link{Id: "x", Dynamic: true, Master: cnet.DefaultBridgeInterface}
		for _, a := range addrs {
			l.Addr = append(l.Addr, mustParseCnetAddr(t, a))
		}
		return cnet.Links{l}
	}
	cont := &config.Config{Name: "web", ContPid: 10, Status: "running", Net: link("192.0.2.23/24", "2001:db8:3:4:192:0:2:23/128", "fd00:db8::23/64")}
	vm := &config.Config{Name: "vm1", HostPid: 11, Status: "running", VM: "kvm", Net: link("2001:db8:3:4::50/128")}
	staleVM := &config.Config{Name: "vm2", HostPid: 12, Status: "running", VM: "kvm", Net: link("2001:db8:1:2::51/128")}
	stopped := &config.Config{Name: "old", ContPid: 0, Status: "exit 0", Net: link("2001:db8:3:4::60/128")}
	bridge := []net.IP{net.ParseIP("2001:db8:3:4::1")}

	got := desiredProxy([]*config.Config{cont, vm, staleVM, stopped}, nil, prefix, bridge)
	want := map[string]bool{"2001:db8:3:4::1": true, "2001:db8:3:4:192:0:2:23": true, "2001:db8:3:4::50": true}
	if len(got) != len(want) {
		t.Fatalf("desiredProxy = %v, want %v", got, want)
	}
	for _, ip := range got {
		if !want[ip.String()] {
			t.Fatalf("unexpected proxy entry %s in %v", ip, got)
		}
	}
}

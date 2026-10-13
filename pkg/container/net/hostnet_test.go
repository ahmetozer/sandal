//go:build linux

package net

import (
	"net"
	"reflect"
	"testing"
)

func TestIPv4ToHextets(t *testing.T) {
	cases := []struct {
		name string
		in   net.IP
		want string
	}{
		{"home RFC1918", net.IPv4(192, 168, 1, 15), "c0a8:10f"},
		{"ten-net low byte", net.IPv4(10, 0, 0, 50), "a00:32"},
		{"CGNAT", net.IPv4(100, 64, 5, 7), "6440:507"},
		{"all zeros", net.IPv4(0, 0, 0, 0), "0:0"},
		{"public WAN", net.IPv4(203, 0, 113, 42), "cb00:712a"},
		{"nil ip", nil, "0:0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ipv4ToHextets(c.in)
			if got != c.want {
				t.Fatalf("ipv4ToHextets(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestPrefixToHextets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"ISP /64", "2001:db8:3b0a:4f00::/64", "2001:db8:3b0a:4f00"},
		{"docs prefix", "2001:db8::/64", "2001:db8:0:0"},
		{"compressed middle", "2001:db8:abcd:1234::/64", "2001:db8:abcd:1234"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, n, err := net.ParseCIDR(c.in)
			if err != nil {
				t.Fatalf("ParseCIDR(%q): %v", c.in, err)
			}
			if got := prefixToHextets(n); got != c.want {
				t.Fatalf("prefixToHextets(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
	if got := prefixToHextets(nil); got != "::" {
		t.Fatalf("prefixToHextets(nil) = %q, want \"::\"", got)
	}
}

func TestSplitHostNet_AllStatic(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return net.IPv4(192, 168, 1, 15) }

	static, dynamic := SplitHostNet("172.16.0.1/24,fd34:0135:0123:0:%uv4%::1/120")
	wantStatic := []string{"172.16.0.1/24", "fd34:0135:0123:0:c0a8:10f::1/120"}
	if !reflect.DeepEqual(static, wantStatic) {
		t.Fatalf("static = %v, want %v", static, wantStatic)
	}
	if len(dynamic) != 0 {
		t.Fatalf("dynamic = %v, want empty", dynamic)
	}
}

func TestSplitHostNet_MixedStaticAndDynamic(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return net.IPv4(192, 168, 1, 15) }

	in := "172.16.0.1/24,fd34:0135:0123:0:%uv4%::1/120,%uv6%:%uv4%::1/64"
	static, dynamic := SplitHostNet(in)
	wantStatic := []string{"172.16.0.1/24", "fd34:0135:0123:0:c0a8:10f::1/120"}
	wantDynamic := []string{"%uv6%:%uv4%::1/64"}
	if !reflect.DeepEqual(static, wantStatic) {
		t.Fatalf("static = %v, want %v", static, wantStatic)
	}
	if !reflect.DeepEqual(dynamic, wantDynamic) {
		t.Fatalf("dynamic = %v, want %v", dynamic, wantDynamic)
	}
}

func TestSplitHostNet_NoIPv4StillPartitions(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return nil }

	in := "172.16.0.1/24,fd34:0135:0123:0:%uv4%::1/120,%uv6%:%uv4%::1/64"
	static, dynamic := SplitHostNet(in)
	wantStatic := []string{"172.16.0.1/24", "fd34:0135:0123:0:0:0::1/120"}
	wantDynamic := []string{"%uv6%:%uv4%::1/64"}
	if !reflect.DeepEqual(static, wantStatic) {
		t.Fatalf("static = %v, want %v", static, wantStatic)
	}
	if !reflect.DeepEqual(dynamic, wantDynamic) {
		t.Fatalf("dynamic = %v, want %v", dynamic, wantDynamic)
	}
	// Fallback must still parse as a valid CIDR.
	if _, _, err := net.ParseCIDR("fd34:0135:0123:0:0:0::1/120"); err != nil {
		t.Fatalf("0:0 fallback must parse: %v", err)
	}
}

func TestSplitHostNet_CaseSensitive(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return net.IPv4(192, 168, 1, 15) }

	// Uppercase tokens must NOT be substituted, and an entry containing
	// only %UV6% counts as static (the lowercase %uv6% trigger isn't there).
	in := "fd34::%UV4%:1/120,%UV6%:%UV4%::1/64"
	static, dynamic := SplitHostNet(in)
	if len(dynamic) != 0 {
		t.Fatalf("dynamic must be empty for uppercase tokens, got %v", dynamic)
	}
	if !reflect.DeepEqual(static, []string{"fd34::%UV4%:1/120", "%UV6%:%UV4%::1/64"}) {
		t.Fatalf("static = %v, want both entries unchanged", static)
	}
}

func TestResolveDynamic_WithPrefix(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return net.IPv4(192, 168, 1, 15) }

	_, prefix, _ := net.ParseCIDR("2001:db8:3b0a:4f00::/64")
	got := ResolveDynamic("%uv6%:%uv4%::1/64", prefix)
	want := "2001:db8:3b0a:4f00:c0a8:10f::1/64"
	if got != want {
		t.Fatalf("ResolveDynamic = %q, want %q", got, want)
	}
	// Result must parse and round-trip via net.IP.Equal.
	ip, _, err := net.ParseCIDR(got)
	if err != nil {
		t.Fatalf("resolved value must parse as CIDR: %v", err)
	}
	wantIP := net.ParseIP("2001:db8:3b0a:4f00:c0a8:10f::1")
	if !ip.Equal(wantIP) {
		t.Fatalf("parsed IP = %s, want %s", ip, wantIP)
	}
}

func TestResolveDynamic_NilPrefixExpandsToDoubleColon(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return net.IPv4(192, 168, 1, 15) }

	// Documented contract: %uv6% -> "::" when prefix is nil; callers MUST
	// withhold the unrouted address rather than apply it.
	got := ResolveDynamic("%uv6%:%uv4%::1/64", nil)
	want := ":::c0a8:10f::1/64"
	if got != want {
		t.Fatalf("ResolveDynamic(nil) = %q, want %q", got, want)
	}
}

func TestResolveDynamic_NoIPv4Substitutes0_0(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return nil }

	_, prefix, _ := net.ParseCIDR("2001:db8:3b0a:4f00::/64")
	got := ResolveDynamic("%uv6%:%uv4%::1/64", prefix)
	want := "2001:db8:3b0a:4f00:0:0::1/64"
	if got != want {
		t.Fatalf("ResolveDynamic(no IPv4) = %q, want %q", got, want)
	}
}

func TestParseDynamicAddr(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"no mask", "%uv6%:192:0:2:23", "%uv6%:192:0:2:23", false},
		{"explicit /128 is stripped", "%uv6%:192:0:2:23/128", "%uv6%:192:0:2:23", false},
		{"compressed IID", "%uv6%::23", "%uv6%::23", false},
		{"non-/128 rejected", "%uv6%:192:0:2:23/64", "", true},
		{"too many hextets", "%uv6%:1:2:3:4:5", "", true},
		{"%uv6% must be the high 64 bits", "fd00::%uv6%", "", true},
		{"literal is not dynamic", "2001:db8::1/128", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseDynamicAddr(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseDynamicAddr(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDynamicAddr(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("parseDynamicAddr(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestResolveDynamicAddr(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return net.IPv4(198, 51, 100, 51) }

	_, prefix, _ := net.ParseCIDR("2001:db8:1:2::/64")
	got, err := ResolveDynamicAddr("%uv6%:192:0:2:23", prefix)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(net.ParseIP("2001:db8:1:2:192:0:2:23")) {
		t.Fatalf("got %s, want 2001:db8:1:2:192:0:2:23", got)
	}
	if _, err := ResolveDynamicAddr("%uv6%:192:0:2:23", nil); err == nil {
		t.Fatal("nil prefix must be an error")
	}
}

func TestDynamicAddrFromIID(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return nil }

	got := dynamicAddrFromIID(net.ParseIP("fd00:db8::23"))
	if got != "%uv6%:0:0:0:23" {
		t.Fatalf("dynamicAddrFromIID = %q, want %%uv6%%:0:0:0:23", got)
	}
	_, prefix, _ := net.ParseCIDR("2001:db8:1:2::/64")
	ip, err := ResolveDynamicAddr(got, prefix)
	if err != nil {
		t.Fatal(err)
	}
	if !ip.Equal(net.ParseIP("2001:db8:1:2::23")) {
		t.Fatalf("resolved %s, want 2001:db8:1:2::23", ip)
	}
}

func mustParseAddr(t *testing.T, s string) Addr {
	t.Helper()
	ip, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return Addr{IP: ip, IPNet: n}
}

func TestStaticBridgeAddrsDropsRenumberManagedGlobal(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return nil }

	in := Addrs{
		mustParseAddr(t, "192.0.2.1/24"),
		mustParseAddr(t, "2001:db8:1:2::1/128"), // renumber-managed (listed first, like the kernel does)
		mustParseAddr(t, "fd00:db8::1/64"),
		mustParseAddr(t, "2001:db8:9::1/64"), // static global declared in SANDAL_HOST_NET
	}
	got := staticBridgeAddrs(in, "192.0.2.1/24,fd00:db8::1/64,2001:db8:9::1/64")
	if len(got) != 3 {
		t.Fatalf("got %d addrs, want 3: %v", len(got), got)
	}
	for _, a := range got {
		if a.IP.Equal(net.ParseIP("2001:db8:1:2::1")) {
			t.Fatalf("renumber-managed global must be dropped, got %v", got)
		}
	}
}

func TestBridgeDynamicPrefix(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return nil }

	hostNet := "192.0.2.1/24,fd00:db8::1/64"
	cases := []struct {
		name       string
		addrs      Addrs
		wantPrefix string
		wantOnes   int
	}{
		{"ndp-proxy /128", Addrs{mustParseAddr(t, "2001:db8:1:2::1/128"), mustParseAddr(t, "fd00:db8::1/64")}, "2001:db8:1:2::/64", 128},
		{"pd /64", Addrs{mustParseAddr(t, "2001:db8:5:6::1/64"), mustParseAddr(t, "fd00:db8::1/64")}, "2001:db8:5:6::/64", 64},
		{"no dynamic global yet", Addrs{mustParseAddr(t, "fd00:db8::1/64")}, "", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, m := bridgeDynamicPrefix(tc.addrs, hostNet)
			if tc.wantPrefix == "" {
				if p != nil {
					t.Fatalf("want nil prefix, got %s", p)
				}
				return
			}
			if p == nil || p.String() != tc.wantPrefix {
				t.Fatalf("prefix = %v, want %s", p, tc.wantPrefix)
			}
			if ones, _ := m.Size(); ones != tc.wantOnes {
				t.Fatalf("mask /%d, want /%d", ones, tc.wantOnes)
			}
		})
	}
}

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
		{"ISP /64", "2a00:1d35:3b0a:4f00::/64", "2a00:1d35:3b0a:4f00"},
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

	_, prefix, _ := net.ParseCIDR("2a00:1d35:3b0a:4f00::/64")
	got := ResolveDynamic("%uv6%:%uv4%::1/64", prefix)
	want := "2a00:1d35:3b0a:4f00:c0a8:10f::1/64"
	if got != want {
		t.Fatalf("ResolveDynamic = %q, want %q", got, want)
	}
	// Result must parse and round-trip via net.IP.Equal.
	ip, _, err := net.ParseCIDR(got)
	if err != nil {
		t.Fatalf("resolved value must parse as CIDR: %v", err)
	}
	wantIP := net.ParseIP("2a00:1d35:3b0a:4f00:c0a8:10f::1")
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

	_, prefix, _ := net.ParseCIDR("2a00:1d35:3b0a:4f00::/64")
	got := ResolveDynamic("%uv6%:%uv4%::1/64", prefix)
	want := "2a00:1d35:3b0a:4f00:0:0::1/64"
	if got != want {
		t.Fatalf("ResolveDynamic(no IPv4) = %q, want %q", got, want)
	}
}

//go:build linux

package net

import (
	"net"
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

func TestResolveHostNet_NoPlaceholder(t *testing.T) {
	in := "172.16.0.1/24,fd34:0135:0123::1/120"
	if got := ResolveHostNet(in); got != in {
		t.Fatalf("ResolveHostNet(%q) = %q, want unchanged", in, got)
	}
}

func TestResolveHostNet_Substitutes(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return net.IPv4(192, 168, 1, 15) }

	in := "172.16.0.1/24,fd34:0135:0123:0:%v4%::1/120"
	want := "172.16.0.1/24,fd34:0135:0123:0:c0a8:10f::1/120"
	if got := ResolveHostNet(in); got != want {
		t.Fatalf("ResolveHostNet(%q) = %q, want %q", in, got, want)
	}
}

func TestResolveHostNet_MultipleOccurrences(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return net.IPv4(10, 0, 0, 50) }

	in := "fd34::%v4%:1/120,fd34::%v4%:2/120"
	want := "fd34::a00:32:1/120,fd34::a00:32:2/120"
	if got := ResolveHostNet(in); got != want {
		t.Fatalf("ResolveHostNet(%q) = %q, want %q", in, got, want)
	}
}

func TestResolveHostNet_NoIPv4(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return nil }

	in := "172.16.0.1/24,fd34:0135:0123:0:%v4%::1/120"
	want := "172.16.0.1/24,fd34:0135:0123:0:0:0::1/120"
	got := ResolveHostNet(in)
	if got != want {
		t.Fatalf("ResolveHostNet(no IPv4) = %q, want %q", got, want)
	}
	if _, _, err := net.ParseCIDR("fd34:0135:0123:0:0:0::1/120"); err != nil {
		t.Fatalf("fallback substitution must still parse as a valid CIDR: %v", err)
	}
}

func TestResolveHostNet_CaseSensitive(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return net.IPv4(192, 168, 1, 15) }

	in := "fd34::%V4%:1/120"
	if got := ResolveHostNet(in); got != in {
		t.Fatalf("ResolveHostNet must not substitute uppercase token: got %q, want unchanged %q", got, in)
	}
}

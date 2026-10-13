//go:build linux

package renumber

import (
	"net"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func mustCIDR(t *testing.T, s string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSelectIgnoresLinkLocalAndULA(t *testing.T) {
	now := time.Now()
	cands := []Candidate{
		{Prefix: mustCIDR(t, "fe80::/64"), ValidUntil: now.Add(time.Hour)},
		{Prefix: mustCIDR(t, "fd00::/64"), ValidUntil: now.Add(time.Hour)},
		{Prefix: mustCIDR(t, "2001:db8::/64"), ValidUntil: now.Add(time.Hour)},
	}
	got := Select(cands, nil)
	if got == nil {
		t.Fatal("expected a global prefix to be selected")
	}
	if got.String() != "2001:db8::/64" {
		t.Errorf("got %s want 2001:db8::/64", got)
	}
}

func TestSelectLongestValidLifetime(t *testing.T) {
	now := time.Now()
	cands := []Candidate{
		{Prefix: mustCIDR(t, "2001:db8:1::/64"), ValidUntil: now.Add(time.Hour)},
		{Prefix: mustCIDR(t, "2001:db8:2::/64"), ValidUntil: now.Add(2 * time.Hour)},
	}
	got := Select(cands, nil)
	if got == nil || got.String() != "2001:db8:2::/64" {
		t.Errorf("got %v want 2001:db8:2::/64", got)
	}
}

func TestSelectStickyToCurrent(t *testing.T) {
	now := time.Now()
	current := mustCIDR(t, "2001:db8:1::/64")
	cands := []Candidate{
		{Prefix: mustCIDR(t, "2001:db8:1::/64"), ValidUntil: now.Add(time.Hour)},
		{Prefix: mustCIDR(t, "2001:db8:2::/64"), ValidUntil: now.Add(time.Hour)},
	}
	got := Select(cands, current)
	if got == nil || got.String() != "2001:db8:1::/64" {
		t.Errorf("got %v want 2001:db8:1::/64 (sticky)", got)
	}
}

func TestSelectSwitchesWhenCurrentDisappears(t *testing.T) {
	now := time.Now()
	current := mustCIDR(t, "2001:db8:1::/64")
	cands := []Candidate{
		{Prefix: mustCIDR(t, "2001:db8:2::/64"), ValidUntil: now.Add(time.Hour)},
	}
	got := Select(cands, current)
	if got == nil || got.String() != "2001:db8:2::/64" {
		t.Errorf("got %v want 2001:db8:2::/64", got)
	}
}

func TestSelectEmpty(t *testing.T) {
	if got := Select(nil, nil); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestIsGlobal(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"2001:db8:1:2::1", true},
		{"fd00:db8::1", false},
		{"fe80::1", false},
		{"192.0.2.1", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isGlobal(net.ParseIP(tc.in)); got != tc.want {
				t.Fatalf("isGlobal(%s) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestIIDEqual(t *testing.T) {
	if !iidEqual(net.ParseIP("2001:db8:1:2:192:0:2:23"), net.ParseIP("2001:db8:3:4:192:0:2:23")) {
		t.Error("same IID under different prefixes must be equal")
	}
	if iidEqual(net.ParseIP("2001:db8:1:2::23"), net.ParseIP("2001:db8:1:2::24")) {
		t.Error("different IIDs must differ")
	}
}

func TestRouteCandidates(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	mk := func(cidr string, proto netlink.RouteProtocol, flags int, gw string) netlink.Route {
		_, dst, _ := net.ParseCIDR(cidr)
		return netlink.Route{Dst: dst, Protocol: proto, Flags: flags, Gw: net.ParseIP(gw)}
	}
	routes := []netlink.Route{
		mk("2001:db8:1:2::/64", unix.RTPROT_KERNEL, 0, ""),               // RA on-link prefix -> candidate
		mk("2001:db8:7::/64", unix.RTPROT_BOOT, 0, ""),                   // user static -> ignored
		mk("fe80::/64", unix.RTPROT_KERNEL, 0, ""),                       // link-local -> ignored
		mk("fd00:db8::/64", unix.RTPROT_KERNEL, 0, ""),                   // ULA -> ignored
		mk("2001:db8:5::/56", unix.RTPROT_KERNEL, 0, ""),                 // not /64 -> ignored
		mk("2001:db8:6::/64", unix.RTPROT_KERNEL, 0, "fe80::1"),          // via gateway -> ignored
		{Dst: nil, Protocol: unix.RTPROT_RA, Gw: net.ParseIP("fe80::1")}, // default -> ignored
	}
	got := routeCandidates(routes, now)
	if len(got) != 1 || got[0].Prefix.String() != "2001:db8:1:2::/64" {
		t.Fatalf("routeCandidates = %v, want only 2001:db8:1:2::/64", got)
	}
	if !got[0].ValidUntil.Equal(now.Add(24 * time.Hour)) {
		t.Fatalf("ValidUntil = %v, want now+24h", got[0].ValidUntil)
	}
}

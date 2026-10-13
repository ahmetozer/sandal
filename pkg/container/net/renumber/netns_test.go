//go:build linux

package renumber

import (
	"net"
	"testing"

	"github.com/vishvananda/netlink"
)

func TestDiffGlobals(t *testing.T) {
	have := []netlink.Addr{
		mustParseAddr(t, "fe80::1/64"),
		mustParseAddr(t, "fd00:db8::23/64"),
		mustParseAddr(t, "2001:db8:1:2:192:0:2:23/128"), // owned, stale prefix
		mustParseAddr(t, "2001:db8:9::7/64"),            // static literal, other IID
		mustParseAddr(t, "2001:db8:3:4::99/128"),        // owned, already wanted
	}
	want := []net.IPNet{
		{IP: net.ParseIP("2001:db8:3:4:192:0:2:23"), Mask: net.CIDRMask(128, 128)},
		{IP: net.ParseIP("2001:db8:3:4::99"), Mask: net.CIDRMask(128, 128)},
	}
	del, add := diffGlobals(have, want)
	if len(del) != 1 || !del[0].IP.Equal(net.ParseIP("2001:db8:1:2:192:0:2:23")) {
		t.Fatalf("del = %v, want only the stale owned global (never LL/ULA/static)", del)
	}
	if len(add) != 1 || !add[0].IP.Equal(net.ParseIP("2001:db8:3:4:192:0:2:23")) {
		t.Fatalf("add = %v, want only the missing address", add)
	}
}

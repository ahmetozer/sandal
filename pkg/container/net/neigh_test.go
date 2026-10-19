//go:build linux

package net

import (
	"net"
	"reflect"
	"testing"
)

// TestBridgedIPs: stale-neighbor flushing must target the bridge each veth
// joins. Links without a host bridge (master=nil, passthru, a KVM guest's
// adopted ethN) contribute nothing, and nil IPs are skipped.
func TestBridgedIPs(t *testing.T) {
	ip := net.ParseIP
	links := Links{
		{Type: LinkTypeVeth, Master: DefaultBridgeInterface, Addr: Addrs{{IP: ip("192.0.2.10")}, {IP: ip("fd00:db8:5::10")}}},
		{Type: LinkTypeVeth, Master: "br-x", Addr: Addrs{{IP: ip("203.0.113.10")}, {IP: nil}}},
		{Type: LinkTypeVeth, Master: "br-x", Addr: Addrs{{IP: ip("203.0.113.11")}}},
		{Type: LinkTypeVeth, Master: "nil", Addr: Addrs{{IP: ip("203.0.113.12")}}},
		{Type: LinkTypeVeth, Master: "", Addr: Addrs{{IP: ip("203.0.113.13")}}},
		{Type: LinkTypePassthru, Master: "", Addr: Addrs{{IP: ip("192.0.2.14")}}},
		{Type: LinkTypePassthru, Master: "br-x", Addr: Addrs{{IP: ip("203.0.113.15")}}},
	}
	want := map[string][]net.IP{
		DefaultBridgeInterface: {ip("192.0.2.10"), ip("fd00:db8:5::10")},
		"br-x":                 {ip("203.0.113.10"), ip("203.0.113.11")},
	}
	if got := links.BridgedIPs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("BridgedIPs() = %v, want %v", got, want)
	}
}

// TestBridgedIPsNoBridge: a KVM guest's links are all passthru without a
// master, so there is nothing to flush and no bridge lookup to fail.
func TestBridgedIPsNoBridge(t *testing.T) {
	links := Links{{Type: LinkTypePassthru, Id: "eth0", Addr: Addrs{{IP: net.ParseIP("192.0.2.20")}}}}
	if got := links.BridgedIPs(); len(got) != 0 {
		t.Fatalf("BridgedIPs() = %v, want empty", got)
	}
}

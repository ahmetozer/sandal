//go:build linux

package net

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"
)

// TestLinkDynamicDefaultsTrueOnMissingKey: legacy configs persisted before
// the Dynamic field existed lack the key entirely. The zero-value of bool
// is false, which would silently opt those containers out of the renumber
// service. The contract: a missing "Dynamic" key must decode to
// Dynamic == true.
func TestLinkDynamicDefaultsTrueOnMissingKey(t *testing.T) {
	raw := []byte(`{"Id":"abc","Name":"eth0"}`)
	var l Link
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	if !l.Dynamic {
		t.Fatal("legacy JSON without Dynamic key should decode to Dynamic=true")
	}
}

// TestLinkDynamicExplicitFalseHonored covers the negation: when a user
// explicitly sets Dynamic=false in the config (e.g. via `-net "...;dynamic=false"`),
// that opt-out must survive a round-trip through JSON.
func TestLinkDynamicExplicitFalseHonored(t *testing.T) {
	raw := []byte(`{"Id":"abc","Name":"eth0","Dynamic":false}`)
	var l Link
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	if l.Dynamic {
		t.Fatal("explicit Dynamic=false must remain false after unmarshal")
	}
}

// TestLinkDynamicExplicitTrueHonored ensures Dynamic=true is preserved.
func TestLinkDynamicExplicitTrueHonored(t *testing.T) {
	raw := []byte(`{"Id":"abc","Dynamic":true}`)
	var l Link
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	if !l.Dynamic {
		t.Fatal("explicit Dynamic=true must remain true after unmarshal")
	}
}

func TestLinkDynamicAddrRoundTrip(t *testing.T) {
	in := Link{Id: "abc", Dynamic: true, DynamicAddr: []string{"%uv6%:192:0:2:23"}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Link
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.DynamicAddr) != 1 || out.DynamicAddr[0] != "%uv6%:192:0:2:23" {
		t.Fatalf("DynamicAddr lost in round trip: %v (json %s)", out.DynamicAddr, raw)
	}
}

func TestLinkDynamicAddrAbsentInLegacyJSON(t *testing.T) {
	var l Link
	if err := json.Unmarshal([]byte(`{"Id":"abc"}`), &l); err != nil {
		t.Fatal(err)
	}
	if l.DynamicAddr != nil {
		t.Fatalf("legacy JSON must decode to nil DynamicAddr, got %v", l.DynamicAddr)
	}
}

func TestAutoDynamicAddr(t *testing.T) {
	allocated := Addrs{mustParseAddr(t, "192.0.2.5/24"), mustParseAddr(t, "fd00:db8::5/64")}
	cases := []struct {
		name string
		in   Link
		want []string
	}{
		{"allocated link mirrors ULA IID", Link{Dynamic: true, Addr: allocated}, []string{"%uv6%:0:0:0:5"}},
		{"explicit dynamic address wins", Link{Dynamic: true, DynamicAddr: []string{"%uv6%:192:0:2:5"}, Addr: allocated}, []string{"%uv6%:192:0:2:5"}},
		{"dynamic=false opts out", Link{Dynamic: false, Addr: allocated}, nil},
		{"dhcp6 opts out", Link{Dynamic: true, DHCPv6: true, Addr: allocated}, nil},
		{"no ULA → nothing", Link{Dynamic: true, Addr: Addrs{mustParseAddr(t, "192.0.2.5/24")}}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := autoDynamicAddr(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("autoDynamicAddr = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveDynamicAtCreate(t *testing.T) {
	orig := upstreamIPv4Fn
	t.Cleanup(func() { upstreamIPv4Fn = orig })
	upstreamIPv4Fn = func() net.IP { return nil }

	_, prefix, _ := net.ParseCIDR("2001:db8:1:2::/64")
	l := Link{Dynamic: true, Master: DefaultBridgeInterface, DynamicAddr: []string{"%uv6%:192:0:2:23"}}
	got := resolveDynamicAtCreate(l, prefix, net.CIDRMask(128, 128))
	if len(got) != 1 || !got[0].IP.Equal(net.ParseIP("2001:db8:1:2:192:0:2:23")) {
		t.Fatalf("got %v", got)
	}
	if ones, _ := got[0].IPNet.Mask.Size(); ones != 128 {
		t.Fatalf("mask /%d, want /128", ones)
	}
	if got := resolveDynamicAtCreate(l, nil, nil); got != nil {
		t.Fatalf("no prefix yet → nothing, got %v", got)
	}
	other := l
	other.Master = "br-other"
	if got := resolveDynamicAtCreate(other, prefix, net.CIDRMask(128, 128)); got != nil {
		t.Fatalf("non-sandal0 master → nothing, got %v", got)
	}
}

// In a KVM guest the host already parsed the same -net flag and resolved its
// %uv6% address and gateways into SANDAL_VM_NET; the guest must use them even
// when ip= lists literal addresses.
func TestDefaultsVMUsesHostResolvedLinks(t *testing.T) {
	host := []Link{{
		Id: "eth0",
		Addr: Addrs{mustParseAddr(t, "192.0.2.40/24"), mustParseAddr(t, "fd00:db8:5::40/64"),
			{IP: net.ParseIP("2001:db8:1:2:192:0:2:40"), IPNet: &net.IPNet{IP: net.ParseIP("2001:db8:1:2:192:0:2:40"), Mask: net.CIDRMask(128, 128)}}},
		Route: Addrs{mustParseAddr(t, "192.0.2.1/24"), mustParseAddr(t, "fd00:db8:5::1/64")},
	}}
	raw, err := json.Marshal(host)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SANDAL_VM", "kvm")
	t.Setenv("SANDAL_VM_NET", string(raw))

	guest := Link{Id: "eth0", Name: "eth0", Dynamic: true,
		Addr:        Addrs{mustParseAddr(t, "192.0.2.40/24"), mustParseAddr(t, "fd00:db8:5::40/64")},
		DynamicAddr: []string{"%uv6%:192:0:2:40"}}
	got := guest.defaults(nil)
	if len(got.Addr) != 3 || !got.Addr[2].IP.Equal(net.ParseIP("2001:db8:1:2:192:0:2:40")) {
		t.Fatalf("guest Addr = %v, want host-resolved addresses incl. the global", got.Addr)
	}
	if len(got.Route) != 2 {
		t.Fatalf("guest Route = %v, want host gateways", got.Route)
	}
	if got.DynamicAddr != nil {
		t.Fatalf("guest cannot resolve %%uv6%%; DynamicAddr must be cleared, got %v", got.DynamicAddr)
	}

	dhcp := Link{Id: "eth0", Name: "eth0", DHCPv4: true}
	if got := dhcp.defaults(nil); len(got.Addr) != 0 {
		t.Fatalf("DHCP links must not take SANDAL_VM_NET addresses, got %v", got.Addr)
	}
}

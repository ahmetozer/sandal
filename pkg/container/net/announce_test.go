//go:build linux

package net

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func TestBuildGratuitousARPFrameLayout(t *testing.T) {
	srcMAC := net.HardwareAddr{0x52, 0x54, 0x00, 0x12, 0x34, 0x56}
	ip := net.ParseIP("192.0.2.7").To4()
	frame := buildGratuitousARP(srcMAC, ip)

	if len(frame) != 42 {
		t.Fatalf("frame length = %d, want 42", len(frame))
	}

	// Ethernet dst = broadcast.
	if !bytes.Equal(frame[0:6], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}) {
		t.Errorf("dst MAC = %x, want broadcast", frame[0:6])
	}
	// Ethernet src = srcMAC.
	if !bytes.Equal(frame[6:12], srcMAC) {
		t.Errorf("src MAC = %x, want %x", frame[6:12], srcMAC)
	}
	// Ethertype = 0x0806.
	if et := binary.BigEndian.Uint16(frame[12:14]); et != 0x0806 {
		t.Errorf("ethertype = %#x, want 0x0806", et)
	}

	// ARP fixed prefix: HTYPE=1, PTYPE=0x0800, HLEN=6, PLEN=4, OPER=1.
	if h := binary.BigEndian.Uint16(frame[14:16]); h != 1 {
		t.Errorf("HTYPE = %d, want 1", h)
	}
	if p := binary.BigEndian.Uint16(frame[16:18]); p != 0x0800 {
		t.Errorf("PTYPE = %#x, want 0x0800", p)
	}
	if frame[18] != 6 || frame[19] != 4 {
		t.Errorf("HLEN/PLEN = %d/%d, want 6/4", frame[18], frame[19])
	}
	if op := binary.BigEndian.Uint16(frame[20:22]); op != 1 {
		t.Errorf("OPER = %d, want 1 (request, RFC 5227)", op)
	}

	// SHA = srcMAC, SPA = ip.
	if !bytes.Equal(frame[22:28], srcMAC) {
		t.Errorf("SHA = %x, want %x", frame[22:28], srcMAC)
	}
	if !bytes.Equal(frame[28:32], ip) {
		t.Errorf("SPA = %s, want %s", net.IP(frame[28:32]), ip)
	}
	// THA must be zero in a gratuitous request.
	if !bytes.Equal(frame[32:38], make([]byte, 6)) {
		t.Errorf("THA = %x, want all zeros", frame[32:38])
	}
	// TPA = SPA.
	if !bytes.Equal(frame[38:42], ip) {
		t.Errorf("TPA = %s, want %s (== SPA for gratuitous)", net.IP(frame[38:42]), ip)
	}
}

func TestBuildUnsolicitedNAPayloadLayout(t *testing.T) {
	srcMAC := net.HardwareAddr{0x02, 0x42, 0xac, 0x11, 0x00, 0x02}
	ip := net.ParseIP("2001:db8::42").To16()
	p := buildUnsolicitedNA(srcMAC, ip)

	if len(p) != 32 {
		t.Fatalf("payload length = %d, want 32", len(p))
	}

	// ICMPv6 type and code.
	if p[0] != 136 {
		t.Errorf("type = %d, want 136 (NA)", p[0])
	}
	if p[1] != 0 {
		t.Errorf("code = %d, want 0", p[1])
	}
	// Checksum is filled by the kernel — must be zero on send.
	if p[2] != 0 || p[3] != 0 {
		t.Errorf("checksum bytes = %#x %#x, want zero (kernel fills)", p[2], p[3])
	}

	// Flags: Override only. Solicited=0, Router=0.
	// Layout (RFC 4861 §4.4): R(7) S(6) O(5) reserved(0..4) — so O=1 alone = 0x20.
	if p[4] != 0x20 {
		t.Errorf("flags byte = %#x, want 0x20 (O=1, S=0, R=0)", p[4])
	}
	if p[5] != 0 || p[6] != 0 || p[7] != 0 {
		t.Errorf("reserved bytes = %x, want zeros", p[5:8])
	}

	// Target address.
	if !bytes.Equal(p[8:24], ip) {
		t.Errorf("target = %s, want %s", net.IP(p[8:24]), ip)
	}

	// Target Link-Layer Address option.
	if p[24] != 2 {
		t.Errorf("option type = %d, want 2 (Target Link-Layer Address)", p[24])
	}
	if p[25] != 1 {
		t.Errorf("option length = %d, want 1 (8 bytes total)", p[25])
	}
	if !bytes.Equal(p[26:32], srcMAC) {
		t.Errorf("option MAC = %x, want %x", p[26:32], srcMAC)
	}
}

func TestHtonsConversion(t *testing.T) {
	cases := []struct {
		in, want uint16
	}{
		{0x0806, 0x0608},
		{0x86dd, 0xdd86},
		{0x0000, 0x0000},
		{0xffff, 0xffff},
		{0x1234, 0x3412},
	}
	for _, tc := range cases {
		if got := htons(tc.in); got != tc.want {
			t.Errorf("htons(%#x) = %#x, want %#x", tc.in, got, tc.want)
		}
	}
}

func v6addr(t *testing.T, cidr string, flags int) netlink.Addr {
	t.Helper()
	a, err := netlink.ParseAddr(cidr)
	if err != nil {
		t.Fatal(err)
	}
	a.Flags = flags
	return *a
}

func TestIPv6AddrState(t *testing.T) {
	addrs := []netlink.Addr{
		v6addr(t, "2001:db8::1/128", 0),
		v6addr(t, "2001:db8::2/128", unix.IFA_F_TENTATIVE),
		v6addr(t, "2001:db8::3/128", unix.IFA_F_TENTATIVE|unix.IFA_F_DADFAILED),
	}
	cases := []struct {
		ip   string
		want addrState
	}{
		{"2001:db8::1", addrReady},
		{"2001:db8::2", addrTentative},
		{"2001:db8::3", addrDADFailed},
		{"2001:db8::4", addrMissing},
	}
	for _, tc := range cases {
		t.Run(tc.ip, func(t *testing.T) {
			if got := ipv6AddrState(addrs, net.ParseIP(tc.ip)); got != tc.want {
				t.Fatalf("ipv6AddrState(%s) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestLinkLocalReady(t *testing.T) {
	if linkLocalReady([]netlink.Addr{v6addr(t, "fe80::1/64", unix.IFA_F_TENTATIVE)}) {
		t.Error("tentative link-local must not be ready")
	}
	if !linkLocalReady([]netlink.Addr{v6addr(t, "fe80::1/64", 0)}) {
		t.Error("non-tentative link-local must be ready")
	}
	if linkLocalReady([]netlink.Addr{v6addr(t, "2001:db8::1/128", 0)}) {
		t.Error("a global is not a link-local source")
	}
}

func TestWaitIPv6ReadyWaitsForDAD(t *testing.T) {
	calls := 0
	list := func() ([]netlink.Addr, error) {
		calls++
		flags := unix.IFA_F_TENTATIVE
		if calls >= 3 {
			flags = 0
		}
		return []netlink.Addr{v6addr(t, "fe80::1/64", flags), v6addr(t, "2001:db8::1/128", flags)}, nil
	}
	ready, skipped := waitIPv6Ready(list, []net.IP{net.ParseIP("2001:db8::1")}, time.Second, time.Millisecond)
	if len(ready) != 1 || len(skipped) != 0 || calls != 3 {
		t.Fatalf("ready=%v skipped=%v calls=%d, want 1 ready after 3 polls", ready, skipped, calls)
	}
}

func TestWaitIPv6ReadySkipsStuckAndFailed(t *testing.T) {
	list := func() ([]netlink.Addr, error) {
		return []netlink.Addr{
			v6addr(t, "fe80::1/64", 0),
			v6addr(t, "2001:db8::1/128", 0),
			v6addr(t, "2001:db8::2/128", unix.IFA_F_TENTATIVE),
			v6addr(t, "2001:db8::3/128", unix.IFA_F_TENTATIVE|unix.IFA_F_DADFAILED),
		}, nil
	}
	targets := []net.IP{net.ParseIP("2001:db8::1"), net.ParseIP("2001:db8::2"), net.ParseIP("2001:db8::3")}
	start := time.Now()
	ready, skipped := waitIPv6Ready(list, targets, 30*time.Millisecond, time.Millisecond)
	if time.Since(start) > time.Second {
		t.Fatal("waitIPv6Ready must honour the timeout")
	}
	if len(ready) != 1 || !ready[0].Equal(net.ParseIP("2001:db8::1")) {
		t.Fatalf("ready = %v, want only 2001:db8::1", ready)
	}
	if skipped["2001:db8::2"] != addrTentative || skipped["2001:db8::3"] != addrDADFailed {
		t.Fatalf("skipped = %v", skipped)
	}
}

func TestWaitIPv6ReadyNeedsLinkLocalSource(t *testing.T) {
	list := func() ([]netlink.Addr, error) {
		return []netlink.Addr{v6addr(t, "fe80::1/64", unix.IFA_F_TENTATIVE), v6addr(t, "2001:db8::1/128", 0)}, nil
	}
	ready, skipped := waitIPv6Ready(list, []net.IP{net.ParseIP("2001:db8::1")}, 20*time.Millisecond, time.Millisecond)
	if len(ready) != 0 || skipped["2001:db8::1"] != addrTentative {
		t.Fatalf("without a usable link-local nothing can be sent: ready=%v skipped=%v", ready, skipped)
	}
}

func TestWaitIPv6ReadyListError(t *testing.T) {
	list := func() ([]netlink.Addr, error) { return nil, errors.New("boom") }
	ready, skipped := waitIPv6Ready(list, []net.IP{net.ParseIP("2001:db8::1")}, 10*time.Millisecond, time.Millisecond)
	if len(ready) != 0 || skipped["2001:db8::1"] != addrMissing {
		t.Fatalf("ready=%v skipped=%v", ready, skipped)
	}
}

func TestAnnounceableIPs(t *testing.T) {
	addrs := Addrs{
		{IP: net.ParseIP("192.0.2.5")},
		{IP: net.ParseIP("169.254.1.1")},
		{IP: net.ParseIP("fe80::1")},
		{IP: net.ParseIP("2001:db8::5")},
		{IP: nil},
	}
	got := announceableIPs(addrs)
	if len(got) != 2 || !got[0].Equal(net.ParseIP("192.0.2.5")) || !got[1].Equal(net.ParseIP("2001:db8::5")) {
		t.Fatalf("announceableIPs = %v, want [192.0.2.5 2001:db8::5]", got)
	}
}

func TestCarriesAll(t *testing.T) {
	have := []netlink.Addr{v6addr(t, "2001:db8::5/64", 0), v6addr(t, "2001:db8:1::5/64", 0)}
	cases := []struct {
		name string
		ips  []net.IP
		want bool
	}{
		{"all present", []net.IP{net.ParseIP("2001:db8::5"), net.ParseIP("2001:db8:1::5")}, true},
		{"one missing", []net.IP{net.ParseIP("2001:db8::5"), net.ParseIP("2001:db8::6")}, false},
		{"empty is false", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := carriesAll(have, tc.ips); got != tc.want {
				t.Fatalf("carriesAll = %v, want %v", got, tc.want)
			}
		})
	}
}

// sandal0 announces itself as the containers' router: R=1 and O=1 (0xA0),
// the flags of the kernel's own NA on a forwarding interface. Everything
// else matches the container announcement, which keeps R=0.
func TestBuildRouterNAFlags(t *testing.T) {
	srcMAC := net.HardwareAddr{0x02, 0x42, 0xac, 0x11, 0x00, 0x02}
	ip := net.ParseIP("fd00:db8:5::1").To16()
	r, c := buildRouterNA(srcMAC, ip), buildUnsolicitedNA(srcMAC, ip)
	if r[4] != 0xa0 {
		t.Errorf("router NA flags byte = %#x, want 0xa0 (R=1, S=0, O=1)", r[4])
	}
	if c[4] != 0x20 {
		t.Errorf("container NA flags byte = %#x, want 0x20 (R=0)", c[4])
	}
	r[4] = c[4]
	if !bytes.Equal(r, c) {
		t.Errorf("router NA %x differs from the container NA %x beyond the flags", r, c)
	}
}

//go:build linux

package net

import (
	"bytes"
	"encoding/binary"
	"net"
	"testing"
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

//go:build linux

package net

import (
	"fmt"
	"net"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// errIsEEXIST must classify "already exists" without panicking on wrapped
// errors. The old code used `err.(unix.Errno)` (single-value assertion), which
// panics for any non-bare-Errno error (e.g. a fmt.wrapError or *os.SyscallError
// returned from the netlink receive path), crashing the process.
func TestErrIsEEXIST(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"bare EEXIST", unix.EEXIST, true},
		{"bare syscall EEXIST", syscall.EEXIST, true},
		{"wrapped EEXIST", fmt.Errorf("add addr: %w", unix.EEXIST), true},
		{"other bare errno", unix.ENOBUFS, false},
		{"wrapped non-errno (would panic old code)", fmt.Errorf("wrong sender portid"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := errIsEEXIST(tc.err); got != tc.want {
				t.Fatalf("errIsEEXIST(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestReplaceGlobalsKeepsStaticGlobals(t *testing.T) {
	mk := func(s string) Addr {
		ip, n, _ := net.ParseCIDR(s)
		return Addr{IP: ip, IPNet: n}
	}
	in := Addrs{
		mk("192.0.2.23/24"),
		mk("2001:db8:1:2:192:0:2:23/128"), // owned dynamic (old prefix)
		mk("2001:db8:9::7/64"),            // static literal global
		mk("fd00:db8::23/64"),
	}
	owned := func(ip net.IP) bool { return ip.Equal(net.ParseIP("2001:db8:1:2:192:0:2:23")) }
	want := []net.IPNet{{IP: net.ParseIP("2001:db8:3:4:192:0:2:23"), Mask: net.CIDRMask(128, 128)}}
	got := in.ReplaceGlobals(owned, want)
	if len(got) != 4 {
		t.Fatalf("got %d addrs, want 4: %v", len(got), got)
	}
	if !got[1].IP.Equal(net.ParseIP("2001:db8:9::7")) || !got[2].IP.Equal(net.ParseIP("fd00:db8::23")) {
		t.Fatalf("static entries must be kept in order, got %v", got)
	}
	if !got[3].IP.Equal(want[0].IP) {
		t.Fatalf("new global missing, got %v", got)
	}
}

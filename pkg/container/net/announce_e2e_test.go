//go:build linux

package net

import (
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

// countFrames reads from fd until it has been idle for idle, counting
// payloads for which match returns true.
func countFrames(t *testing.T, fd int, idle time.Duration, match func([]byte) bool) int {
	t.Helper()
	tv := unix.NsecToTimeval(idle.Nanoseconds())
	if err := unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 1500)
	n := 0
	for {
		l, _, err := unix.Recvfrom(fd, buf, 0)
		if err != nil {
			return n
		}
		if match(buf[:l]) {
			n++
		}
	}
}

// AnnounceLinkAddrs must actually emit count GARPs and NAs per address
// (IPv6 only after DAD), synchronously and within the time budget.
func TestAnnounceLinkAddrs_E2E(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("e2e: requires root")
	}
	runtime.LockOSThread()
	orig, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer orig.Close()
	restored := false
	defer func() {
		// Only release the thread once it is back in its original netns.
		if restored {
			runtime.UnlockOSThread()
		}
	}()
	ns, err := netns.New() // switches this thread into a fresh netns
	if err != nil {
		t.Skipf("e2e: cannot create netns: %v", err)
	}
	defer func() {
		if err := netns.Set(orig); err != nil {
			t.Errorf("e2e: restoring netns: %v", err)
		} else {
			restored = true
		}
		ns.Close()
	}()

	if err := netlink.LinkAdd(&netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: "annA"}, PeerName: "annB"}); err != nil {
		t.Skipf("e2e: cannot create veth: %v", err)
	}
	a, err := netlink.LinkByName("annA")
	if err != nil {
		t.Fatal(err)
	}
	b, err := netlink.LinkByName("annB")
	if err != nil {
		t.Fatal(err)
	}
	if err := netlink.LinkSetUp(b); err != nil {
		t.Fatal(err)
	}

	// Listeners on the peer: ICMPv6 (NA) and ARP.
	fd6, err := unix.Socket(unix.AF_INET6, unix.SOCK_RAW, unix.IPPROTO_ICMPV6)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd6)
	if err := unix.BindToDevice(fd6, "annB"); err != nil {
		t.Fatal(err)
	}
	fd4, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_ARP)))
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd4)
	if err := unix.Bind(fd4, &unix.SockaddrLinklayer{Protocol: htons(unix.ETH_P_ARP), Ifindex: b.Attrs().Index}); err != nil {
		t.Fatal(err)
	}

	v4, _ := netlink.ParseAddr("192.0.2.5/24")
	v6, _ := netlink.ParseAddr("2001:db8:a::1/64") // DAD on (netns default) → tentative at first
	for _, x := range []*netlink.Addr{v4, v6} {
		if err := netlink.AddrAdd(a, x); err != nil {
			t.Fatal(err)
		}
	}
	// A host default arp_notify=1 would add a kernel ARP announcement.
	// The kernel uses max(conf/all, conf/<dev>), so clear both.
	for _, p := range []string{"annA", "all"} {
		if err := os.WriteFile("/proc/sys/net/ipv4/conf/"+p+"/arp_notify", []byte("0"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := netlink.LinkSetUp(a); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	AnnounceLinkAddrs("annA", Addrs{{IP: v4.IP, IPNet: v4.IPNet}, {IP: v6.IP, IPNet: v6.IPNet}})
	d := time.Since(start)
	t.Logf("AnnounceLinkAddrs took %v", d)
	if d > 4*time.Second {
		t.Fatalf("AnnounceLinkAddrs took %v, want ≤ 4s (DAD wait 3s + rounds)", d)
	}

	target := net.ParseIP("2001:db8:a::1").To16()
	nas := countFrames(t, fd6, 500*time.Millisecond, func(p []byte) bool {
		return len(p) >= 24 && p[0] == 136 && p[4]&0x20 != 0 && net.IP(p[8:24]).Equal(target)
	})
	t.Logf("NAs=%d", nas)
	if nas != 3 {
		t.Fatalf("received %d NAs for %s, want 3", nas, target)
	}
	spa := net.ParseIP("192.0.2.5").To4()
	garps := countFrames(t, fd4, 500*time.Millisecond, func(f []byte) bool {
		return len(f) >= 42 && net.IP(f[28:32]).Equal(spa) && net.IP(f[38:42]).Equal(spa)
	})
	t.Logf("GARPs=%d", garps)
	if garps != 3 {
		t.Fatalf("received %d gratuitous ARPs for %s, want 3", garps, spa)
	}
}

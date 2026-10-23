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

// annLab is a throw-away netns with a veth pair: the code under test
// announces on annA; listeners on the peer annB count what arrives.
type annLab struct {
	ns       netns.NsHandle
	a        netlink.Link
	fd4, fd6 int
}

// setupAnnounceLab locks the test goroutine's thread, moves it into a fresh
// netns with veth annA<->annB (annB up, kernel ARP announcements disabled)
// and opens ARP + ICMPv6 listeners on annB. t.Cleanup undoes everything; the
// thread is only unlocked after the original netns was restored.
func setupAnnounceLab(t *testing.T) *annLab {
	t.Helper()
	if os.Getuid() != 0 {
		t.Skip("e2e: requires root")
	}
	runtime.LockOSThread()
	orig, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}
	ns, err := netns.New()
	if err != nil {
		orig.Close()
		t.Skipf("e2e: cannot create netns: %v", err)
	}
	t.Cleanup(func() {
		err := netns.Set(orig)
		ns.Close()
		orig.Close()
		if err != nil {
			t.Errorf("restore netns: %v", err)
			return // keep the thread locked; it is discarded with the goroutine
		}
		runtime.UnlockOSThread()
	})

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
	// A host default arp_notify=1 would add a kernel ARP announcement.
	// The kernel uses max(conf/all, conf/<dev>), so clear both.
	for _, f := range []string{"/proc/sys/net/ipv4/conf/all/arp_notify", "/proc/sys/net/ipv4/conf/annA/arp_notify"} {
		if err := os.WriteFile(f, []byte("0"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fd6, err := unix.Socket(unix.AF_INET6, unix.SOCK_RAW, unix.IPPROTO_ICMPV6)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(fd6) })
	if err := unix.BindToDevice(fd6, "annB"); err != nil {
		t.Fatal(err)
	}
	fd4, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_ARP)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(fd4) })
	if err := unix.Bind(fd4, &unix.SockaddrLinklayer{Protocol: htons(unix.ETH_P_ARP), Ifindex: b.Attrs().Index}); err != nil {
		t.Fatal(err)
	}
	return &annLab{ns: ns, a: a, fd4: fd4, fd6: fd6}
}

func labAddrs(t *testing.T) (v4, v6 *netlink.Addr, addrs Addrs) {
	t.Helper()
	v4, _ = netlink.ParseAddr("192.0.2.5/24")
	v6, _ = netlink.ParseAddr("2001:db8:a::1/64") // DAD on (fresh netns default)
	return v4, v6, Addrs{{IP: v4.IP, IPNet: v4.IPNet}, {IP: v6.IP, IPNet: v6.IPNet}}
}

func countNAs(t *testing.T, fd int, target string) int {
	t.Helper()
	ip := net.ParseIP(target).To16()
	return countFrames(t, fd, 500*time.Millisecond, func(p []byte) bool {
		return len(p) >= 24 && p[0] == 136 && p[4]&0x20 != 0 && net.IP(p[8:24]).Equal(ip)
	})
}

func countGARPs(t *testing.T, fd int, spa string) int {
	t.Helper()
	ip := net.ParseIP(spa).To4()
	return countFrames(t, fd, 500*time.Millisecond, func(f []byte) bool {
		return len(f) >= 42 && net.IP(f[28:32]).Equal(ip) && net.IP(f[38:42]).Equal(ip)
	})
}

// AnnounceLinkAddrs must actually emit count GARPs and NAs per address
// (IPv6 only after DAD), synchronously and within the time budget.
func TestAnnounceLinkAddrs_E2E(t *testing.T) {
	lab := setupAnnounceLab(t)
	v4, v6, addrs := labAddrs(t)
	for _, x := range []*netlink.Addr{v4, v6} {
		if err := netlink.AddrAdd(lab.a, x); err != nil {
			t.Fatal(err)
		}
	}
	if err := netlink.LinkSetUp(lab.a); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	AnnounceLinkAddrs("annA", addrs)
	d := time.Since(start)
	t.Logf("AnnounceLinkAddrs took %v", d)
	if d > 4*time.Second {
		t.Fatalf("AnnounceLinkAddrs took %v, want <= 4s (DAD wait 3s + rounds)", d)
	}
	nas := countNAs(t, lab.fd6, "2001:db8:a::1")
	t.Logf("NAs=%d", nas)
	if nas != 3 {
		t.Fatalf("NAs = %d, want 3", nas)
	}
	garps := countGARPs(t, lab.fd4, "192.0.2.5")
	t.Logf("GARPs=%d", garps)
	if garps != 3 {
		t.Fatalf("GARPs = %d, want 3", garps)
	}
}

// AnnounceARP is what container init calls right before exec: one GARP per
// IPv4 address, no NA, and it must not wait (not even for DAD).
func TestAnnounceARP_E2E(t *testing.T) {
	lab := setupAnnounceLab(t)
	v4, v6, addrs := labAddrs(t)
	for _, x := range []*netlink.Addr{v4, v6} {
		if err := netlink.AddrAdd(lab.a, x); err != nil {
			t.Fatal(err)
		}
	}
	if err := netlink.LinkSetUp(lab.a); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	AnnounceARP("annA", addrs)
	d := time.Since(start)
	t.Logf("AnnounceARP took %v", d)
	if d > 200*time.Millisecond {
		t.Fatalf("AnnounceARP took %v, must not wait", d)
	}
	garps := countGARPs(t, lab.fd4, "192.0.2.5")
	t.Logf("GARPs=%d", garps)
	if garps != 1 {
		t.Fatalf("GARPs = %d, want 1", garps)
	}
	nas := countNAs(t, lab.fd6, "2001:db8:a::1")
	t.Logf("NAs=%d", nas)
	if nas != 0 {
		t.Fatalf("NAs = %d, want 0 (IPv6 is the long-lived process's job)", nas)
	}
}

// announceRun is the outcome of announceFromFreshThread.
type announceRun struct {
	fresh    bool // the goroutine's thread started outside the target netns
	restored bool // the thread was back in its starting netns afterwards
	took     time.Duration
	err      error
}

// announceFromFreshThread runs AnnounceInNetns the way the daemon does: from a
// new goroutine whose thread starts in the original netns (the test
// goroutine's thread is locked inside the lab netns), so the netns switch is
// real. The thread is only handed back to the scheduler when it is verified
// to be back where it started; otherwise the goroutine exits still locked and
// the runtime discards the thread.
func announceFromFreshThread(ns netns.NsHandle, links Links) <-chan announceRun {
	ch := make(chan announceRun, 1)
	go func() {
		var r announceRun
		runtime.LockOSThread()
		before, err := netns.Get()
		if err != nil {
			r.err = err
			ch <- r
			return
		}
		defer before.Close()
		start := time.Now()
		AnnounceInNetns(ns, links)
		r.took = time.Since(start)
		after, err := netns.Get()
		if err != nil {
			r.err = err
			ch <- r
			return
		}
		defer after.Close()
		r.fresh = !before.Equal(ns)
		r.restored = after.Equal(before)
		if r.fresh && r.restored {
			runtime.UnlockOSThread()
		}
		ch <- r
	}()
	return ch
}

func checkFreshRun(t *testing.T, r announceRun) {
	t.Helper()
	if r.err != nil {
		t.Fatal(r.err)
	}
	if !r.fresh {
		t.Fatal("announcing thread already was in the lab netns: not a cross-netns test")
	}
	if !r.restored {
		t.Fatal("AnnounceInNetns did not restore the thread's netns")
	}
}

// AnnounceInNetns runs in the daemon / VM init while the container's init is
// still configuring: it must wait for the link to carry the addresses (found
// by address, the link's name is unknown to the host), then announce. It is
// called from a thread outside the lab netns, so entering and leaving the
// container netns is exercised for real.
func TestAnnounceInNetns_E2E(t *testing.T) {
	lab := setupAnnounceLab(t)
	v4, v6, addrs := labAddrs(t)
	if err := netlink.LinkSetUp(lab.a); err != nil {
		t.Fatal(err)
	}
	h, err := netlink.NewHandleAt(lab.ns)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	res := announceFromFreshThread(lab.ns, Links{{Id: "host-side-name", Addr: addrs}})
	time.Sleep(300 * time.Millisecond) // addresses appear only after AnnounceInNetns started
	addErr := h.AddrAdd(lab.a, v4)
	if addErr == nil {
		addErr = h.AddrAdd(lab.a, v6)
	}
	r := <-res
	if addErr != nil {
		t.Fatal(addErr)
	}
	checkFreshRun(t, r)
	t.Logf("AnnounceInNetns took %v", r.took)
	if r.took > 5*time.Second {
		t.Fatalf("AnnounceInNetns took %v, want <= 5s", r.took)
	}
	nas := countNAs(t, lab.fd6, "2001:db8:a::1")
	t.Logf("NAs=%d", nas)
	if nas != 3 {
		t.Fatalf("NAs = %d, want 3", nas)
	}
	garps := countGARPs(t, lab.fd4, "192.0.2.5")
	t.Logf("GARPs=%d", garps)
	if garps != 3 {
		t.Fatalf("GARPs = %d, want 3", garps)
	}
}

// Container init adds the addresses first and brings the link up last: a link
// that carries every address but is still down must not be announced on yet
// (the sends would fail with ENETDOWN and the announcement would be lost).
func TestAnnounceInNetnsWaitsForLinkUp_E2E(t *testing.T) {
	lab := setupAnnounceLab(t) // annA stays DOWN
	v4, _, _ := labAddrs(t)
	h, err := netlink.NewHandleAt(lab.ns)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	res := announceFromFreshThread(lab.ns, Links{{Id: "host-side-name", Addr: Addrs{{IP: v4.IP, IPNet: v4.IPNet}}}})
	time.Sleep(300 * time.Millisecond)
	stepErr := h.AddrAdd(lab.a, v4) // addresses complete, link still down
	time.Sleep(300 * time.Millisecond)
	if stepErr == nil {
		stepErr = h.LinkSetUp(lab.a)
	}
	r := <-res
	if stepErr != nil {
		t.Fatal(stepErr)
	}
	checkFreshRun(t, r)
	t.Logf("AnnounceInNetns took %v", r.took)
	if r.took > 5*time.Second {
		t.Fatalf("AnnounceInNetns took %v, want <= 5s", r.took)
	}
	garps := countGARPs(t, lab.fd4, "192.0.2.5")
	t.Logf("GARPs=%d", garps)
	if garps != 3 {
		t.Fatalf("GARPs = %d, want 3 (announced only once the link was up)", garps)
	}
	nas := countNAs(t, lab.fd6, "2001:db8:a::1")
	t.Logf("NAs=%d", nas)
	if nas != 0 {
		t.Fatalf("NAs = %d, want 0 (no IPv6 address configured)", nas)
	}
}

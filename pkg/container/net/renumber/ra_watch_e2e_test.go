//go:build linux

package renumber

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

// raLabRouter sends crafted RAs out of one veth end and counts the Router
// Solicitations that arrive there.
type raLabRouter struct {
	tx, rx int
}

// setupRALab locks the test goroutine's thread into a fresh netns (DAD off)
// with veth pairs raA<->raB (raA is the uplink) and oA<->oB (another link).
// It returns a router on raB and one on oB.
func setupRALab(t *testing.T) (uplink, other *raLabRouter) {
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
	for _, f := range []string{"/proc/sys/net/ipv6/conf/all/accept_dad", "/proc/sys/net/ipv6/conf/default/accept_dad"} {
		if err := os.WriteFile(f, []byte("0"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk := func(a, b string) *raLabRouter {
		if err := netlink.LinkAdd(&netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: a}, PeerName: b}); err != nil {
			t.Skipf("e2e: cannot create veth: %v", err)
		}
		var peer netlink.Link
		for _, n := range []string{a, b} {
			l, err := netlink.LinkByName(n)
			if err != nil {
				t.Fatal(err)
			}
			if err := netlink.LinkSetUp(l); err != nil {
				t.Fatal(err)
			}
			peer = l
		}
		// The router side needs its link-local address to source RAs.
		deadline := time.Now().Add(3 * time.Second)
		for {
			addrs, _ := netlink.AddrList(peer, netlink.FAMILY_V6)
			if len(addrs) > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: no link-local address", b)
			}
			time.Sleep(20 * time.Millisecond)
		}
		r := &raLabRouter{}
		for _, fd := range []*int{&r.tx, &r.rx} {
			*fd, err = unix.Socket(unix.AF_INET6, unix.SOCK_RAW, unix.IPPROTO_ICMPV6)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { unix.Close(*fd) })
			if err := unix.BindToDevice(*fd, b); err != nil {
				t.Fatal(err)
			}
		}
		if err := unix.SetsockoptInt(r.tx, unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_HOPS, 255); err != nil {
			t.Fatal(err)
		}
		if err := unix.SetsockoptInt(r.tx, unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_IF, peer.Attrs().Index); err != nil {
			t.Fatal(err)
		}
		// Not a router (forwarding off): join all-routers to see RSs.
		mreq := &unix.IPv6Mreq{Multiaddr: [16]byte{0xff, 0x02, 15: 0x02}, Interface: uint32(peer.Attrs().Index)}
		if err := unix.SetsockoptIPv6Mreq(r.rx, unix.IPPROTO_IPV6, unix.IPV6_JOIN_GROUP, mreq); err != nil {
			t.Fatal(err)
		}
		tv := unix.NsecToTimeval((300 * time.Millisecond).Nanoseconds())
		if err := unix.SetsockoptTimeval(r.rx, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv); err != nil {
			t.Fatal(err)
		}
		return r
	}
	return mk("raA", "raB"), mk("oA", "oB")
}

func (r *raLabRouter) send(t *testing.T, opts ...[]byte) {
	t.Helper()
	if err := unix.Sendto(r.tx, buildRA(opts...), 0, &unix.SockaddrInet6{Addr: [16]byte{0xff, 0x02, 15: 0x01}}); err != nil {
		t.Fatal(err)
	}
}

// solicitations counts the RSs received so far.
func (r *raLabRouter) solicitations() int {
	buf := make([]byte, 1500)
	n := 0
	for {
		l, _, err := unix.Recvfrom(r.rx, buf, 0)
		if err != nil {
			return n
		}
		if l >= 8 && buf[0] == 133 {
			n++
		}
	}
}

// readFor runs the source's RA reader on fd in this (netns-locked) goroutine.
func readFor(t *testing.T, s *RASource, fd int, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	if err := s.readRAs(ctx, fd); err != nil {
		t.Fatalf("readRAs: %v", err)
	}
}

// The router renumbers from P1 to P2 without withdrawing P1: the kernel keeps
// P1's on-link route, but after two RAs without it, the second one after the
// Router Solicitation sent on the first, and once that RS is raConfirmDelay
// old, the source must move to P2. RAs on another link are ignored.
func TestRASourceAbandonsForgottenPrefix_E2E(t *testing.T) {
	uplink, other := setupRALab(t)
	s := NewRASource("raA")
	fd, err := openRASocket()
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(fd)

	uplink.send(t, pioOpt("2001:db8:aaaa:1::", 64, pioL, 300))
	deadline := time.Now().Add(2 * time.Second)
	for {
		if p := s.poll(); p != nil && p.String() == raP1 {
			s.setCurrent(p)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("kernel did not install P1 from the RA")
		}
		time.Sleep(20 * time.Millisecond)
	}
	readFor(t, s, fd, 1200*time.Millisecond)

	other.send(t, pioOpt("2001:db8:bbbb:1::", 64, pioL, 300))
	other.send(t, pioOpt("2001:db8:bbbb:1::", 64, pioL, 300))
	readFor(t, s, fd, 1200*time.Millisecond)
	if got := s.poll(); got == nil || got.String() != raP1 {
		t.Fatalf("after RAs on another link: poll = %v, want %s", got, raP1)
	}

	uplink.send(t, pioOpt("2001:db8:bbbb:1::", 64, pioL, 300))
	rsSent := time.Now()
	readFor(t, s, fd, 300*time.Millisecond) // returns within ~1 s (1 s SO_RCVTIMEO)
	if n := uplink.solicitations(); n != 1 {
		t.Fatalf("RS after the first RA without P1 = %d, want 1", n)
	}
	if got := s.poll(); got == nil || got.String() != raP1 {
		t.Fatalf("after one RA without P1: poll = %v, want %s (never switch on a single RA)", got, raP1)
	}

	// The second RA completes the evidence, but the RS is still too young:
	// no switch yet. Both reads are short, so this happens about 2.5 s after
	// the RS; fail rather than skip the check if it is ever too late.
	uplink.send(t, pioOpt("2001:db8:bbbb:1::", 64, pioL, 300))
	readFor(t, s, fd, 300*time.Millisecond)
	elapsed := time.Since(rsSent)
	t.Logf("second RA without P1 handled %v after the RS", elapsed)
	if elapsed >= raConfirmDelay-500*time.Millisecond {
		t.Fatalf("second RA handled %v after the RS: too late to check that the switch waits %v for the answers", elapsed, raConfirmDelay)
	}
	if got := s.poll(); got == nil || got.String() != raP1 {
		t.Fatalf("%v after the RS: poll = %v, want %s (wait for all answers)", elapsed, got, raP1)
	}
	if d := raConfirmDelay + 500*time.Millisecond - time.Since(rsSent); d > 0 {
		readFor(t, s, fd, d)
	}
	link, err := netlink.LinkByName("raA")
	if err != nil {
		t.Fatal(err)
	}
	routes, err := netlink.RouteList(link, netlink.FAMILY_V6)
	if err != nil {
		t.Fatal(err)
	}
	if c := routeCandidates(routes, time.Now()); len(c) != 2 {
		t.Fatalf("kernel candidates = %v, want P1 and P2 (P1 not withdrawn)", c)
	}
	if got := s.poll(); got == nil || got.String() != raP2 {
		t.Fatalf("after two RAs without P1 and %v since the RS: poll = %v, want %s", time.Since(rsSent), got, raP2)
	}
}

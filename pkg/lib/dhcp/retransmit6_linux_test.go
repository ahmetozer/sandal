//go:build linux

package dhcp

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

// silentConn counts WriteTo calls and never delivers a packet.
type silentConn struct {
	net.PacketConn
	mu     sync.Mutex
	writes [][]byte
}

func (s *silentConn) WriteTo(b []byte, _ net.Addr) (int, error) {
	s.mu.Lock()
	s.writes = append(s.writes, append([]byte(nil), b...))
	s.mu.Unlock()
	return len(b), nil
}
func (s *silentConn) SetReadDeadline(t time.Time) error { return nil }
func (s *silentConn) ReadFrom([]byte) (int, net.Addr, error) {
	time.Sleep(2 * time.Millisecond)
	return 0, nil, &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}
}

// shrinkRT scales all retransmission timers down for the test and removes jitter.
func shrinkRT(t *testing.T) {
	t.Helper()
	oS, oQ, oN, oB, oR := solicitRT, requestRT, renewRT, rebindRT, rtRand
	solicitRT = rtParams{IRT: 200 * time.Millisecond, MRT: time.Second}
	requestRT = rtParams{IRT: 200 * time.Millisecond, MRT: time.Second, MRC: 10}
	renewRT = rtParams{IRT: 200 * time.Millisecond, MRT: time.Second}
	rebindRT = renewRT
	rtRand = func() float64 { return 0 }
	t.Cleanup(func() { solicitRT, requestRT, renewRT, rebindRT, rtRand = oS, oQ, oN, oB, oR })
}

func TestExchangeStopsAtMRC(t *testing.T) {
	shrinkRT(t)
	c := &Client6{iface: &net.Interface{Index: 1, Name: "lo"}, ServerPort: 547}
	conn := &silentConn{}
	pkt := &Packet6{MsgType: Msg6Request, TransactionID: [3]byte{1, 2, 3}, Options: Options6{Option6ElapsedTime(0)}}
	p := rtParams{IRT: 10 * time.Millisecond, MRT: 40 * time.Millisecond, MRC: 4}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := c.exchange(ctx, conn, pkt, Msg6Reply, p)
	if err == nil || !strings.Contains(err.Error(), "4 transmissions") {
		t.Fatalf("err = %v, want MRC failure after 4 transmissions", err)
	}
	if len(conn.writes) != 4 {
		t.Fatalf("writes = %d, want 4", len(conn.writes))
	}
	var prev uint16
	for i, w := range conn.writes {
		q, err := Unmarshal6(w)
		if err != nil || q.TransactionID != pkt.TransactionID {
			t.Fatalf("write %d: bad packet / txid changed (%v)", i, err)
		}
		el := binary.BigEndian.Uint16(q.Options.Get(Opt6ElapsedTime))
		if i == 0 && el != 0 {
			t.Fatalf("first elapsed = %d, want 0", el)
		}
		if i > 0 && el <= prev {
			t.Fatalf("write %d elapsed %d not > previous %d", i, el, prev)
		}
		prev = el
	}
}

func TestExchangeStopsOnContext(t *testing.T) {
	shrinkRT(t)
	c := &Client6{iface: &net.Interface{Index: 1, Name: "lo"}, ServerPort: 547}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := c.exchange(ctx, &silentConn{}, &Packet6{MsgType: Msg6Solicit}, Msg6Advertise, solicitRT)
	if err == nil || ctx.Err() == nil {
		t.Fatalf("err = %v, want ctx deadline", err)
	}
}

// fakeServer is a minimal DHCPv6 server on annB. It drops the first
// dropCount packets of type dropType and records everything it receives.
type fakeServer struct {
	conn      net.PacketConn
	pd        bool
	dropType  byte
	dropCount int

	mu   sync.Mutex
	seen []*Packet6
	at   []time.Time
}

func (s *fakeServer) count(msgType byte) (n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.seen {
		if p.MsgType == msgType {
			n++
		}
	}
	return
}

func (s *fakeServer) packets(msgType byte) (out []*Packet6) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.seen {
		if p.MsgType == msgType {
			out = append(out, p)
		}
	}
	return
}

func (s *fakeServer) serve() {
	sid := []byte{0, 3, 0, 1, 2, 2, 2, 2, 2, 2}
	buf := make([]byte, 1500)
	dropped := 0
	for {
		n, from, err := s.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		p, err := Unmarshal6(buf[:n])
		if err != nil {
			continue
		}
		s.mu.Lock()
		s.seen = append(s.seen, p)
		s.mu.Unlock()
		if p.MsgType == s.dropType && dropped < s.dropCount {
			dropped++
			continue
		}
		var rt byte
		switch p.MsgType {
		case Msg6Solicit:
			rt = Msg6Advertise
		case Msg6Request, Msg6Renew:
			rt = Msg6Reply
		default:
			continue
		}
		opts := Options6{Option6ServerID(sid), Option6ClientID(p.Options.ClientID())}
		if s.pd {
			pfx := Option6IAPrefixOpt(IAPrefix{PreferredLifetime: 3600, ValidLifetime: 7200, PrefixLength: 60, Prefix: net.ParseIP("2001:db8:100::")})
			opts = append(opts, Option6IAPDOpt(&IAPD{IAID: 1, T1: 1800, T2: 2880, Options: Options6{pfx}}))
		} else {
			addr := Option6IAAddr(net.ParseIP("2001:db8:100::5"), 3600, 7200)
			opts = append(opts, Option6IANAOpt(&IANA{IAID: 1, T1: 1800, T2: 2880, Options: Options6{addr}}))
		}
		reply := &Packet6{MsgType: rt, TransactionID: p.TransactionID, Options: opts}
		s.conn.WriteTo(reply.Marshal(), from)
	}
}

// dhcpLab: fresh netns with veth dhA<->dhB; client on dhA, fake server on dhB.
func dhcpLab(t *testing.T, srv *fakeServer) *Client6 {
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
			return
		}
		runtime.UnlockOSThread()
	})
	if err := netlink.LinkAdd(&netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: "dhA"}, PeerName: "dhB"}); err != nil {
		t.Skipf("e2e: cannot create veth: %v", err)
	}
	for _, n := range []string{"dhA", "dhB"} {
		l, err := netlink.LinkByName(n)
		if err != nil {
			t.Fatal(err)
		}
		if err := netlink.LinkSetUp(l); err != nil {
			t.Fatal(err)
		}
	}
	b, _ := netlink.LinkByName("dhB")

	var ctlErr error
	lcfg := net.ListenConfig{Control: func(_, _ string, rc syscall.RawConn) error {
		rc.Control(func(fd uintptr) {
			if ctlErr = unix.BindToDevice(int(fd), "dhB"); ctlErr != nil {
				return
			}
			ctlErr = unix.SetsockoptIPv6Mreq(int(fd), unix.IPPROTO_IPV6, unix.IPV6_JOIN_GROUP,
				&unix.IPv6Mreq{Multiaddr: [16]byte{0xff, 0x02, 13: 1, 15: 2}, Interface: uint32(b.Attrs().Index)})
		})
		return ctlErr
	}}
	conn, err := lcfg.ListenPacket(context.Background(), "udp6", "[::]:10547")
	if err != nil {
		t.Fatal(err)
	}
	srv.conn = conn
	go srv.serve()
	t.Cleanup(func() { conn.Close() })

	c, err := NewClient6("dhA")
	if err != nil {
		t.Fatal(err)
	}
	c.ClientPort, c.ServerPort = 10546, 10547
	return c
}

func checkRetransmit(t *testing.T, srv *fakeServer, msgType byte) {
	t.Helper()
	ps := srv.packets(msgType)
	if len(ps) < 2 {
		t.Fatalf("server saw %d %s packets, want >=2", len(ps), MessageType6(msgType))
	}
	first, second := ps[0], ps[1]
	if first.TransactionID != second.TransactionID {
		t.Fatalf("txID changed on retransmit: %x -> %x", first.TransactionID, second.TransactionID)
	}
	e1 := binary.BigEndian.Uint16(first.Options.Get(Opt6ElapsedTime))
	e2 := binary.BigEndian.Uint16(second.Options.Get(Opt6ElapsedTime))
	if e1 != 0 || e2 <= e1 {
		t.Fatalf("elapsed first=%d second=%d, want 0 then larger", e1, e2)
	}
}

func TestE2EPDRequestRetransmitted(t *testing.T) {
	shrinkRT(t)
	srv := &fakeServer{pd: true, dropType: Msg6Request, dropCount: 1}
	c := dhcpLab(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	start := time.Now()
	lease, err := c.ObtainPDLease(ctx, &PrefixHint{Length: 60})
	if err != nil {
		t.Fatalf("ObtainPDLease: %v (after %v)", err, time.Since(start))
	}
	if lease.CIDR() != "2001:db8:100::/60" {
		t.Fatalf("prefix = %s", lease.CIDR())
	}
	checkRetransmit(t, srv, Msg6Request)
}

func TestE2EPDSolicitRetransmitted(t *testing.T) {
	shrinkRT(t)
	srv := &fakeServer{pd: true, dropType: Msg6Solicit, dropCount: 1}
	c := dhcpLab(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := c.ObtainPDLease(ctx, nil); err != nil {
		t.Fatalf("ObtainPDLease: %v", err)
	}
	checkRetransmit(t, srv, Msg6Solicit)
}

func TestE2ENARequestRetransmitted(t *testing.T) {
	shrinkRT(t)
	srv := &fakeServer{dropType: Msg6Request, dropCount: 1}
	c := dhcpLab(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	lease, err := c.ObtainLease(ctx)
	if err != nil {
		t.Fatalf("ObtainLease: %v", err)
	}
	if lease.ClientIP.String() != "2001:db8:100::5" {
		t.Fatalf("ip = %v", lease.ClientIP)
	}
	checkRetransmit(t, srv, Msg6Request)
}

func TestE2ENASolicitRetransmitted(t *testing.T) {
	shrinkRT(t)
	srv := &fakeServer{dropType: Msg6Solicit, dropCount: 1}
	c := dhcpLab(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := c.ObtainLease(ctx); err != nil {
		t.Fatalf("ObtainLease: %v", err)
	}
	checkRetransmit(t, srv, Msg6Solicit)
}

// Renew is answered through the same helper; the server answers only the
// second RENEW.
func TestE2EPDRenewRetransmitted(t *testing.T) {
	shrinkRT(t)
	srv := &fakeServer{pd: true, dropType: Msg6Renew, dropCount: 1}
	c := dhcpLab(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, pfx, _ := net.ParseCIDR("2001:db8:100::/60")
	if _, err := c.RenewPDLease(ctx, &PDLease{Prefix: pfx, ServerDUID: []byte{0, 3, 0, 1, 2, 2, 2, 2, 2, 2}}); err != nil {
		t.Fatalf("RenewPDLease: %v", err)
	}
	checkRetransmit(t, srv, Msg6Renew)
}

// pdLoopRefresher drives RenewPDLease from a LeaseLoop.
type pdLoopRefresher struct {
	c       *Client6
	lease   *PDLease
	renewed chan *PDLease
}

func (r *pdLoopRefresher) Renew(ctx context.Context) (time.Duration, time.Duration, time.Duration, error) {
	l, err := r.c.RenewPDLease(ctx, r.lease)
	if err != nil {
		return 0, 0, 0, err
	}
	r.lease = l
	select {
	case r.renewed <- l:
	default:
	}
	return l.T1, l.T2, l.ValidLifetime, nil
}

func (r *pdLoopRefresher) Rebind(ctx context.Context) (time.Duration, time.Duration, time.Duration, error) {
	return 0, 0, 0, context.DeadlineExceeded
}

func (r *pdLoopRefresher) Release() error { return nil }

// The lease loop must let exchange retransmit a lost Renew (same txID,
// growing Elapsed Time) instead of cutting each attempt short and starting
// a fresh transaction. Renew IRT is kept above the loop's old 2 s attempt cap.
func TestE2ELeaseLoopRenewRetransmitted(t *testing.T) {
	shrinkRT(t)
	renewRT = rtParams{IRT: 2500 * time.Millisecond, MRT: 5 * time.Second}
	srv := &fakeServer{pd: true, dropType: Msg6Renew, dropCount: 1}
	c := dhcpLab(t, srv)
	_, pfx, _ := net.ParseCIDR("2001:db8:100::/60")
	r := &pdLoopRefresher{c: c, lease: &PDLease{Prefix: pfx, ServerDUID: []byte{0, 3, 0, 1, 2, 2, 2, 2, 2, 2}}, renewed: make(chan *PDLease, 1)}
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	var got *PDLease
	go func() {
		select {
		case got = <-r.renewed:
		case <-ctx.Done():
		}
		cancel()
	}()
	// Run on this goroutine: its OS thread is locked into the test netns.
	loop := NewLeaseLoop(r, LeaseTimes{T1: 50 * time.Millisecond, T2: 8 * time.Second, Valid: 10 * time.Second})
	loop.Run(ctx)
	if got == nil {
		t.Fatalf("lease not renewed; server saw %d RENEW", srv.count(Msg6Renew))
	}
	if got.CIDR() != "2001:db8:100::/60" {
		t.Fatalf("renewed prefix = %s", got.CIDR())
	}
	checkRetransmit(t, srv, Msg6Renew)
}

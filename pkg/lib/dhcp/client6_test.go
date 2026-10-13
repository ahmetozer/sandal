//go:build linux

package dhcp

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

// flakyConn fails the first n WriteTo calls with EADDRNOTAVAIL (tentative
// link-local during DAD), then succeeds.
type flakyConn struct {
	net.PacketConn
	failFirst int
	writes    int
}

func (f *flakyConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	f.writes++
	if f.writes <= f.failFirst {
		return 0, &net.OpError{Op: "sendto", Err: syscall.EADDRNOTAVAIL}
	}
	return len(b), nil
}

func TestSendUntilRetriesTransientSendErrors(t *testing.T) {
	c := &flakyConn{failFirst: 2}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	dst := func() *net.UDPAddr { return &net.UDPAddr{IP: net.ParseIP("ff02::1:2"), Port: 547} }
	if err := sendUntil(ctx, c, &Packet6{MsgType: Msg6Solicit}, dst, 10*time.Millisecond); err != nil {
		t.Fatalf("sendUntil: %v", err)
	}
	if c.writes != 3 {
		t.Fatalf("writes = %d, want 3 (2 failures + success)", c.writes)
	}
}

func TestSendUntilGivesUpWhenContextEnds(t *testing.T) {
	c := &flakyConn{failFirst: 1 << 30}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	dst := func() *net.UDPAddr { return &net.UDPAddr{IP: net.ParseIP("ff02::1:2"), Port: 547} }
	err := sendUntil(ctx, c, &Packet6{MsgType: Msg6Solicit}, dst, 10*time.Millisecond)
	if err == nil || !errors.Is(err, syscall.EADDRNOTAVAIL) {
		t.Fatalf("err = %v, want wrapped EADDRNOTAVAIL after ctx end", err)
	}
}

// The link is renamed after the client is created (temp veth name → eth0);
// the client must follow the index, not the stale name.
func TestCurrentIfaceFollowsIndexAfterRename(t *testing.T) {
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		t.Skip("no loopback")
	}
	stale := *lo
	stale.Name = "tmp-old-name"
	c := &Client6{iface: &stale}
	if got := c.currentIface().Name; got != "lo" {
		t.Fatalf("currentIface().Name = %q, want %q", got, "lo")
	}
	if got := c.serverAddr().Zone; got != "lo" {
		t.Fatalf("serverAddr().Zone = %q, want %q", got, "lo")
	}
}

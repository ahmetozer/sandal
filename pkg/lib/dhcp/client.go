package dhcp

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"syscall"
	"time"
)

// Lease holds the result of a successful DHCP exchange.
type Lease struct {
	ClientIP   net.IP
	SubnetMask net.IPMask
	Router     net.IP
	DNS        []net.IP
	ServerIP   net.IP
	LeaseTime  time.Duration
}

// CIDR returns the leased IP in CIDR notation (e.g. "192.168.64.3/24").
func (l *Lease) CIDR() string {
	ones, _ := l.SubnetMask.Size()
	return fmt.Sprintf("%s/%d", l.ClientIP, ones)
}

// IPNet returns the IP network derived from this lease.
func (l *Lease) IPNet() *net.IPNet {
	return &net.IPNet{IP: l.ClientIP, Mask: l.SubnetMask}
}

// Client is a DHCP client bound to a network interface.
type Client struct {
	iface      *net.Interface
	Timeout    time.Duration
	ClientPort int // UDP port to listen on (default 68)
	ServerPort int // UDP port to send to (default 67)
}

// NewClient creates a DHCP client for the named network interface.
func NewClient(ifName string) (*Client, error) {
	iface, err := net.InterfaceByName(ifName)
	if err != nil {
		return nil, fmt.Errorf("dhcp: interface %q: %w", ifName, err)
	}
	if len(iface.HardwareAddr) == 0 {
		return nil, fmt.Errorf("dhcp: interface %q has no hardware address", ifName)
	}
	return &Client{
		iface:      iface,
		Timeout:    10 * time.Second,
		ClientPort: 68,
		ServerPort: 67,
	}, nil
}

// ObtainLease performs a full DORA exchange (Discover → Offer → Request → Ack).
func (c *Client) ObtainLease(ctx context.Context) (*Lease, error) {
	conn, err := c.listen()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	xid, err := randomXID()
	if err != nil {
		return nil, err
	}

	// DISCOVER
	if err := sendBroadcast(conn, c.newDiscover(xid), c.broadcastAddr()); err != nil {
		return nil, fmt.Errorf("dhcp: DISCOVER: %w", err)
	}

	// Wait for OFFER
	offer, err := c.recv(ctx, conn, xid, MsgOffer)
	if err != nil {
		return nil, fmt.Errorf("dhcp: waiting OFFER: %w", err)
	}

	// REQUEST
	if err := sendBroadcast(conn, c.newRequest(xid, offer), c.broadcastAddr()); err != nil {
		return nil, fmt.Errorf("dhcp: REQUEST: %w", err)
	}

	// Wait for ACK
	ack, err := c.recv(ctx, conn, xid, MsgAck)
	if err != nil {
		return nil, fmt.Errorf("dhcp: waiting ACK: %w", err)
	}

	return toLease(ack), nil
}

// RenewLease unicasts a REQUEST to renew an existing lease.
func (c *Client) RenewLease(ctx context.Context, lease *Lease) (*Lease, error) {
	conn, err := c.listen()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	xid, err := randomXID()
	if err != nil {
		return nil, err
	}

	renew := &Packet{
		Op: OpRequest, HType: HTypeEthernet,
		HLen:   byte(len(c.iface.HardwareAddr)),
		XID:    xid,
		CIAddr: lease.ClientIP,
		CHAddr: c.iface.HardwareAddr,
		Options: Options{
			OptionMessageType(MsgRequest),
			OptionClientID(HTypeEthernet, c.iface.HardwareAddr),
		},
	}
	if err := sendTo(conn, renew, &net.UDPAddr{IP: lease.ServerIP, Port: c.ServerPort}); err != nil {
		return nil, fmt.Errorf("dhcp: RENEW: %w", err)
	}

	ack, err := c.recv(ctx, conn, xid, MsgAck)
	if err != nil {
		return nil, fmt.Errorf("dhcp: waiting ACK: %w", err)
	}
	return toLease(ack), nil
}

// ReleaseLease informs the server that the lease is no longer needed.
func (c *Client) ReleaseLease(lease *Lease) error {
	conn, err := c.listen()
	if err != nil {
		return err
	}
	defer conn.Close()

	xid, err := randomXID()
	if err != nil {
		return err
	}

	release := &Packet{
		Op: OpRequest, HType: HTypeEthernet,
		HLen:   byte(len(c.iface.HardwareAddr)),
		XID:    xid,
		CIAddr: lease.ClientIP,
		CHAddr: c.iface.HardwareAddr,
		Options: Options{
			OptionMessageType(MsgRelease),
			OptionServerID(lease.ServerIP),
		},
	}
	return sendTo(conn, release, &net.UDPAddr{IP: lease.ServerIP, Port: c.ServerPort})
}

// Internal packet builders

func (c *Client) newDiscover(xid uint32) *Packet {
	return &Packet{
		Op: OpRequest, HType: HTypeEthernet,
		HLen:   byte(len(c.iface.HardwareAddr)),
		XID:    xid,
		Flags:  FlagBroadcast,
		CHAddr: c.iface.HardwareAddr,
		Options: Options{
			OptionMessageType(MsgDiscover),
			OptionClientID(HTypeEthernet, c.iface.HardwareAddr),
			OptionParamRequestList(OptSubnetMask, OptRouter, OptDNS, OptLeaseTime, OptDomainName),
		},
	}
}

func (c *Client) newRequest(xid uint32, offer *Packet) *Packet {
	return &Packet{
		Op: OpRequest, HType: HTypeEthernet,
		HLen:   byte(len(c.iface.HardwareAddr)),
		XID:    xid,
		Flags:  FlagBroadcast,
		CHAddr: c.iface.HardwareAddr,
		Options: Options{
			OptionMessageType(MsgRequest),
			OptionClientID(HTypeEthernet, c.iface.HardwareAddr),
			OptionRequestedIP(offer.YIAddr),
			OptionServerID(offer.Options.ServerID()),
			OptionParamRequestList(OptSubnetMask, OptRouter, OptDNS, OptLeaseTime, OptDomainName),
		},
	}
}

// Socket management

func (c *Client) listen() (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, rc syscall.RawConn) error {
			var sockErr error
			rc.Control(func(fd uintptr) {
				sockErr = setSockOpts(fd, c.iface)
			})
			return sockErr
		},
	}
	return lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf("0.0.0.0:%d", c.ClientPort))
}

func (c *Client) recv(ctx context.Context, conn net.PacketConn, xid uint32, want MessageType) (*Packet, error) {
	buf := make([]byte, 1500)
	for {
		// Use short deadline so we can check context cancellation
		conn.SetReadDeadline(time.Now().Add(time.Second))
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			return nil, err
		}
		pkt, err := Unmarshal(buf[:n])
		if err != nil {
			continue // skip malformed packets
		}
		if pkt.XID != xid || pkt.Op != OpReply {
			continue
		}
		mt := pkt.Options.MessageType()
		if mt == MsgNak {
			return nil, fmt.Errorf("dhcp: server sent NAK")
		}
		if mt == want {
			return pkt, nil
		}
	}
}

func (c *Client) broadcastAddr() *net.UDPAddr {
	return &net.UDPAddr{IP: net.IPv4bcast, Port: c.ServerPort}
}

func sendBroadcast(conn net.PacketConn, pkt *Packet, addr *net.UDPAddr) error {
	_, err := conn.WriteTo(pkt.Marshal(), addr)
	return err
}

func sendTo(conn net.PacketConn, pkt *Packet, addr *net.UDPAddr) error {
	_, err := conn.WriteTo(pkt.Marshal(), addr)
	return err
}

// Helpers

func toLease(pkt *Packet) *Lease {
	l := &Lease{
		ClientIP:   pkt.YIAddr,
		ServerIP:   pkt.Options.ServerID(),
		SubnetMask: pkt.Options.SubnetMask(),
		Router:     pkt.Options.Router(),
		DNS:        pkt.Options.DNS(),
	}
	if t := pkt.Options.LeaseTime(); t > 0 {
		l.LeaseTime = time.Duration(t) * time.Second
	}
	if l.SubnetMask == nil {
		l.SubnetMask = net.CIDRMask(24, 32)
	}
	return l
}

func randomXID() (uint32, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("dhcp: random XID: %w", err)
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

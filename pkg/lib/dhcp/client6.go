package dhcp

import (
	"context"
	"crypto/rand"
	"fmt"
	"net"
	"syscall"
	"time"
)

// Lease6 holds the result of a successful DHCPv6 exchange.
type Lease6 struct {
	ClientIP          net.IP
	PreferredLifetime time.Duration
	ValidLifetime     time.Duration
	DNS               []net.IP
	ServerDUID        []byte
	T1                time.Duration // Renewal time
	T2                time.Duration // Rebinding time
}

// CIDR returns the leased IPv6 address as /128 (DHCPv6 assigns single addresses).
func (l *Lease6) CIDR() string {
	return fmt.Sprintf("%s/128", l.ClientIP)
}

// IPNet returns the IP network for this lease (single host /128).
func (l *Lease6) IPNet() *net.IPNet {
	return &net.IPNet{IP: l.ClientIP, Mask: net.CIDRMask(128, 128)}
}

// Client6 is a DHCPv6 client bound to a network interface.
type Client6 struct {
	iface      *net.Interface
	duid       []byte
	Timeout    time.Duration
	ClientPort int // UDP port to listen on (default 546)
	ServerPort int // UDP port to send to (default 547)
}

// NewClient6 creates a DHCPv6 client for the named network interface.
// Uses DUID-LL (link-layer) as the client identifier.
func NewClient6(ifName string) (*Client6, error) {
	iface, err := net.InterfaceByName(ifName)
	if err != nil {
		return nil, fmt.Errorf("dhcp6: interface %q: %w", ifName, err)
	}
	if len(iface.HardwareAddr) == 0 {
		return nil, fmt.Errorf("dhcp6: interface %q has no hardware address", ifName)
	}
	return &Client6{
		iface:      iface,
		duid:       DUIDLL(1, iface.HardwareAddr), // hardware type 1 = ethernet
		Timeout:    10 * time.Second,
		ClientPort: 546,
		ServerPort: 547,
	}, nil
}

// ObtainLease performs a full SARR exchange (Solicit → Advertise → Request → Reply).
func (c *Client6) ObtainLease(ctx context.Context) (*Lease6, error) {
	conn, err := c.listen()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	txID := randomTxID()

	// SOLICIT
	if err := send6(conn, c.newSolicit(txID), c.serverAddr()); err != nil {
		return nil, fmt.Errorf("dhcp6: SOLICIT: %w", err)
	}

	// Wait for ADVERTISE
	adv, err := c.recv6(ctx, conn, txID, Msg6Advertise)
	if err != nil {
		return nil, fmt.Errorf("dhcp6: waiting ADVERTISE: %w", err)
	}

	// REQUEST
	if err := send6(conn, c.newRequest(txID, adv), c.serverAddr()); err != nil {
		return nil, fmt.Errorf("dhcp6: REQUEST: %w", err)
	}

	// Wait for REPLY
	reply, err := c.recv6(ctx, conn, txID, Msg6Reply)
	if err != nil {
		return nil, fmt.Errorf("dhcp6: waiting REPLY: %w", err)
	}

	return toLease6(reply)
}

// RenewLease sends a Renew to extend an existing lease.
func (c *Client6) RenewLease(ctx context.Context, lease *Lease6) (*Lease6, error) {
	conn, err := c.listen()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	txID := randomTxID()

	renew := &Packet6{
		MsgType:       Msg6Renew,
		TransactionID: txID,
		Options: Options6{
			Option6ClientID(c.duid),
			Option6ServerID(lease.ServerDUID),
			Option6ElapsedTime(0),
			Option6IANAOpt(&IANA{
				IAID: 1,
				Options: Options6{
					Option6IAAddr(lease.ClientIP, 0, 0),
				},
			}),
			Option6ORO(Opt6DNSServers),
		},
	}
	if err := send6(conn, renew, c.serverAddr()); err != nil {
		return nil, fmt.Errorf("dhcp6: RENEW: %w", err)
	}

	reply, err := c.recv6(ctx, conn, txID, Msg6Reply)
	if err != nil {
		return nil, fmt.Errorf("dhcp6: waiting REPLY: %w", err)
	}
	return toLease6(reply)
}

// ReleaseLease informs the server that the lease is no longer needed.
func (c *Client6) ReleaseLease(lease *Lease6) error {
	conn, err := c.listen()
	if err != nil {
		return err
	}
	defer conn.Close()

	txID := randomTxID()

	release := &Packet6{
		MsgType:       Msg6Release,
		TransactionID: txID,
		Options: Options6{
			Option6ClientID(c.duid),
			Option6ServerID(lease.ServerDUID),
			Option6IANAOpt(&IANA{
				IAID: 1,
				Options: Options6{
					Option6IAAddr(lease.ClientIP, 0, 0),
				},
			}),
		},
	}
	return send6(conn, release, c.serverAddr())
}

// Internal packet builders

func (c *Client6) newSolicit(txID [3]byte) *Packet6 {
	return &Packet6{
		MsgType:       Msg6Solicit,
		TransactionID: txID,
		Options: Options6{
			Option6ClientID(c.duid),
			Option6ElapsedTime(0),
			Option6IANAOpt(&IANA{IAID: 1}),
			Option6ORO(Opt6DNSServers),
		},
	}
}

func (c *Client6) newRequest(txID [3]byte, adv *Packet6) *Packet6 {
	opts := Options6{
		Option6ClientID(c.duid),
		Option6ElapsedTime(0),
		Option6ORO(Opt6DNSServers),
	}
	// Copy server ID from advertise
	if sid := adv.Options.ServerID(); sid != nil {
		opts = append(opts, Option6ServerID(sid))
	}
	// Copy IA_NA from advertise (includes offered address)
	if iaData := adv.Options.Get(Opt6IANA); iaData != nil {
		d := make([]byte, len(iaData))
		copy(d, iaData)
		opts = append(opts, Option6{Code: Opt6IANA, Data: d})
	}
	return &Packet6{
		MsgType:       Msg6Request,
		TransactionID: txID,
		Options:       opts,
	}
}

// Socket and I/O

// serverAddr returns the All_DHCP_Relay_Agents_and_Servers multicast address
// scoped to the client's interface.
func (c *Client6) serverAddr() *net.UDPAddr {
	return &net.UDPAddr{
		IP:   net.ParseIP("ff02::1:2"),
		Port: c.ServerPort,
		Zone: c.iface.Name,
	}
}

func (c *Client6) listen() (net.PacketConn, error) {
	lc := net.ListenConfig{
		Control: func(network, address string, rc syscall.RawConn) error {
			var sockErr error
			rc.Control(func(fd uintptr) {
				sockErr = setSockOpts6(fd, c.iface)
			})
			return sockErr
		},
	}
	return lc.ListenPacket(context.Background(), "udp6", fmt.Sprintf("[::]:%d", c.ClientPort))
}

func (c *Client6) recv6(ctx context.Context, conn net.PacketConn, txID [3]byte, want byte) (*Packet6, error) {
	buf := make([]byte, 1500)
	for {
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
		pkt, err := Unmarshal6(buf[:n])
		if err != nil {
			continue
		}
		if pkt.TransactionID != txID {
			continue
		}
		if pkt.MsgType == want {
			return pkt, nil
		}
	}
}

func send6(conn net.PacketConn, pkt *Packet6, addr *net.UDPAddr) error {
	_, err := conn.WriteTo(pkt.Marshal(), addr)
	return err
}

// Helpers

func toLease6(pkt *Packet6) (*Lease6, error) {
	ia := pkt.Options.IANA()
	if ia == nil {
		return nil, fmt.Errorf("dhcp6: reply has no IA_NA")
	}
	addrs := ia.Addresses()
	if len(addrs) == 0 {
		// Check for status code
		code, msg := ia.Options.StatusCode()
		if code != Status6Success {
			return nil, fmt.Errorf("dhcp6: IA_NA status %d: %s", code, msg)
		}
		return nil, fmt.Errorf("dhcp6: IA_NA has no addresses")
	}

	l := &Lease6{
		ClientIP:          addrs[0].IP,
		PreferredLifetime: time.Duration(addrs[0].PreferredLifetime) * time.Second,
		ValidLifetime:     time.Duration(addrs[0].ValidLifetime) * time.Second,
		ServerDUID:        pkt.Options.ServerID(),
		DNS:               pkt.Options.DNS(),
		T1:                time.Duration(ia.T1) * time.Second,
		T2:                time.Duration(ia.T2) * time.Second,
	}
	return l, nil
}

func randomTxID() [3]byte {
	var b [3]byte
	rand.Read(b[:])
	return b
}

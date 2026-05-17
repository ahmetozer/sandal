package dhcp

import (
	"context"
	"fmt"
	"net"
	"time"
)

// PDLease holds the result of a successful DHCPv6-PD exchange.
type PDLease struct {
	Prefix            *net.IPNet // delegated prefix (e.g. 2001:db8::/60)
	PreferredLifetime time.Duration
	ValidLifetime     time.Duration
	ServerDUID        []byte
	T1                time.Duration
	T2                time.Duration
}

// CIDR returns the delegated prefix in CIDR notation.
func (l *PDLease) CIDR() string {
	if l.Prefix == nil {
		return ""
	}
	return l.Prefix.String()
}

// PrefixHint optionally requests a specific prefix length from the server.
type PrefixHint struct {
	Length uint8 // requested prefix length (0 = no hint)
}

// ObtainPDLease performs a SARR exchange with IA_PD instead of IA_NA.
func (c *Client6) ObtainPDLease(ctx context.Context, hint *PrefixHint) (*PDLease, error) {
	conn, err := c.listen()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	txID := randomTxID()

	solicit := c.newPDSolicit(txID, hint)
	adv, err := c.solicitAdvertise(ctx, conn, txID, solicit)
	if err != nil {
		return nil, err
	}

	if err := send6(conn, c.newPDRequest(txID, adv), c.serverAddr()); err != nil {
		return nil, fmt.Errorf("dhcp6-pd: REQUEST: %w", err)
	}

	reply, err := c.recv6(ctx, conn, txID, Msg6Reply)
	if err != nil {
		return nil, fmt.Errorf("dhcp6-pd: waiting REPLY: %w", err)
	}
	return toPDLease(reply)
}

// RenewPDLease unicasts a Renew for an existing PD binding.
func (c *Client6) RenewPDLease(ctx context.Context, lease *PDLease) (*PDLease, error) {
	conn, err := c.listen()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	txID := randomTxID()
	pl := lease.Prefix
	ones, _ := pl.Mask.Size()
	renew := &Packet6{
		MsgType:       Msg6Renew,
		TransactionID: txID,
		Options: Options6{
			Option6ClientID(c.duid),
			Option6ServerID(lease.ServerDUID),
			Option6ElapsedTime(0),
			Option6IAPDOpt(&IAPD{
				IAID: 1,
				Options: Options6{
					Option6IAPrefixOpt(IAPrefix{
						PrefixLength: uint8(ones),
						Prefix:       pl.IP.To16(),
					}),
				},
			}),
		},
	}
	if err := send6(conn, renew, c.serverAddr()); err != nil {
		return nil, fmt.Errorf("dhcp6-pd: RENEW: %w", err)
	}
	reply, err := c.recv6(ctx, conn, txID, Msg6Reply)
	if err != nil {
		return nil, fmt.Errorf("dhcp6-pd: waiting REPLY: %w", err)
	}
	return toPDLease(reply)
}

// RebindPDLease multicasts a Rebind (server-id absent).
func (c *Client6) RebindPDLease(ctx context.Context, lease *PDLease) (*PDLease, error) {
	conn, err := c.listen()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	txID := randomTxID()
	pl := lease.Prefix
	ones, _ := pl.Mask.Size()
	rebind := &Packet6{
		MsgType:       Msg6Rebind,
		TransactionID: txID,
		Options: Options6{
			Option6ClientID(c.duid),
			Option6ElapsedTime(0),
			Option6IAPDOpt(&IAPD{
				IAID: 1,
				Options: Options6{
					Option6IAPrefixOpt(IAPrefix{
						PrefixLength: uint8(ones),
						Prefix:       pl.IP.To16(),
					}),
				},
			}),
		},
	}
	if err := send6(conn, rebind, c.serverAddr()); err != nil {
		return nil, fmt.Errorf("dhcp6-pd: REBIND: %w", err)
	}
	reply, err := c.recv6(ctx, conn, txID, Msg6Reply)
	if err != nil {
		return nil, fmt.Errorf("dhcp6-pd: waiting REPLY: %w", err)
	}
	return toPDLease(reply)
}

// ReleasePDLease informs the server the delegation is no longer needed.
func (c *Client6) ReleasePDLease(lease *PDLease) error {
	conn, err := c.listen()
	if err != nil {
		return err
	}
	defer conn.Close()

	txID := randomTxID()
	pl := lease.Prefix
	ones, _ := pl.Mask.Size()
	release := &Packet6{
		MsgType:       Msg6Release,
		TransactionID: txID,
		Options: Options6{
			Option6ClientID(c.duid),
			Option6ServerID(lease.ServerDUID),
			Option6IAPDOpt(&IAPD{
				IAID: 1,
				Options: Options6{
					Option6IAPrefixOpt(IAPrefix{
						PrefixLength: uint8(ones),
						Prefix:       pl.IP.To16(),
					}),
				},
			}),
		},
	}
	return send6(conn, release, c.serverAddr())
}

func (c *Client6) newPDSolicit(txID [3]byte, hint *PrefixHint) *Packet6 {
	iaPD := &IAPD{IAID: 1}
	if hint != nil && hint.Length > 0 {
		iaPD.Options = Options6{
			Option6IAPrefixOpt(IAPrefix{PrefixLength: hint.Length, Prefix: net.IPv6zero}),
		}
	}
	return &Packet6{
		MsgType:       Msg6Solicit,
		TransactionID: txID,
		Options: Options6{
			Option6ClientID(c.duid),
			Option6ElapsedTime(0),
			Option6IAPDOpt(iaPD),
			Option6ORO(Opt6DNSServers),
		},
	}
}

func (c *Client6) newPDRequest(txID [3]byte, adv *Packet6) *Packet6 {
	opts := Options6{
		Option6ClientID(c.duid),
		Option6ElapsedTime(0),
	}
	if sid := adv.Options.ServerID(); sid != nil {
		opts = append(opts, Option6ServerID(sid))
	}
	if iaData := adv.Options.Get(Opt6IAPD); iaData != nil {
		d := make([]byte, len(iaData))
		copy(d, iaData)
		opts = append(opts, Option6{Code: Opt6IAPD, Data: d})
	}
	return &Packet6{
		MsgType:       Msg6Request,
		TransactionID: txID,
		Options:       opts,
	}
}

func toPDLease(pkt *Packet6) (*PDLease, error) {
	ia := pkt.Options.IAPD()
	if ia == nil {
		return nil, fmt.Errorf("dhcp6-pd: reply has no IA_PD")
	}
	prefixes := ia.Prefixes()
	if len(prefixes) == 0 {
		code, msg := ia.Options.StatusCode()
		if code != Status6Success {
			return nil, fmt.Errorf("dhcp6-pd: IA_PD status %d: %s", code, msg)
		}
		return nil, fmt.Errorf("dhcp6-pd: IA_PD has no prefixes")
	}
	p := prefixes[0]
	return &PDLease{
		Prefix: &net.IPNet{
			IP:   p.Prefix,
			Mask: net.CIDRMask(int(p.PrefixLength), 128),
		},
		PreferredLifetime: time.Duration(p.PreferredLifetime) * time.Second,
		ValidLifetime:     time.Duration(p.ValidLifetime) * time.Second,
		ServerDUID:        pkt.Options.ServerID(),
		T1:                time.Duration(ia.T1) * time.Second,
		T2:                time.Duration(ia.T2) * time.Second,
	}, nil
}

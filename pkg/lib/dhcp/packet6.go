package dhcp

import (
	"fmt"
)

// DHCPv6 message types (RFC 8415)
const (
	Msg6Solicit     byte = 1
	Msg6Advertise   byte = 2
	Msg6Request     byte = 3
	Msg6Confirm     byte = 4
	Msg6Renew       byte = 5
	Msg6Rebind      byte = 6
	Msg6Reply       byte = 7
	Msg6Release     byte = 8
	Msg6Decline     byte = 9
	Msg6InfoRequest byte = 11
)

// MessageType6 returns the human-readable name for a DHCPv6 message type.
func MessageType6(mt byte) string {
	names := map[byte]string{
		Msg6Solicit: "SOLICIT", Msg6Advertise: "ADVERTISE", Msg6Request: "REQUEST",
		Msg6Confirm: "CONFIRM", Msg6Renew: "RENEW", Msg6Rebind: "REBIND",
		Msg6Reply: "REPLY", Msg6Release: "RELEASE", Msg6Decline: "DECLINE",
		Msg6InfoRequest: "INFORMATION-REQUEST",
	}
	if s, ok := names[mt]; ok {
		return s
	}
	return fmt.Sprintf("UNKNOWN(%d)", mt)
}

// Packet6 represents a DHCPv6 message (RFC 8415 §8).
//
// Wire format: msg-type (1) + transaction-id (3) + options (variable).
type Packet6 struct {
	MsgType       byte
	TransactionID [3]byte
	Options       Options6
}

// Marshal serializes the DHCPv6 packet to wire format.
func (p *Packet6) Marshal() []byte {
	buf := make([]byte, 4)
	buf[0] = p.MsgType
	copy(buf[1:4], p.TransactionID[:])
	buf = append(buf, p.Options.Marshal()...)
	return buf
}

// Unmarshal6 parses a DHCPv6 packet from wire format.
func Unmarshal6(data []byte) (*Packet6, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("dhcp6: packet too short (%d bytes)", len(data))
	}
	p := &Packet6{
		MsgType: data[0],
	}
	copy(p.TransactionID[:], data[1:4])
	var err error
	p.Options, err = ParseOptions6(data[4:])
	if err != nil {
		return nil, err
	}
	return p, nil
}

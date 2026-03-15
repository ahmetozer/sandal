package dhcp

import (
	"encoding/binary"
	"fmt"
	"net"
)

// DHCP option codes (RFC 2132)
const (
	OptPad              byte = 0
	OptSubnetMask       byte = 1
	OptRouter           byte = 3
	OptDNS              byte = 6
	OptHostname         byte = 12
	OptDomainName       byte = 15
	OptBroadcastAddr    byte = 28
	OptRequestedIP      byte = 50
	OptLeaseTime        byte = 51
	OptMessageType      byte = 53
	OptServerID         byte = 54
	OptParamRequestList byte = 55
	OptRenewalTime      byte = 58
	OptRebindingTime    byte = 59
	OptClientID         byte = 61
	OptEnd              byte = 255
)

// MessageType represents a DHCP message type (option 53).
type MessageType byte

const (
	MsgDiscover MessageType = 1
	MsgOffer    MessageType = 2
	MsgRequest  MessageType = 3
	MsgDecline  MessageType = 4
	MsgAck      MessageType = 5
	MsgNak      MessageType = 6
	MsgRelease  MessageType = 7
)

func (m MessageType) String() string {
	names := [...]string{0: "", 1: "DISCOVER", 2: "OFFER", 3: "REQUEST",
		4: "DECLINE", 5: "ACK", 6: "NAK", 7: "RELEASE"}
	if int(m) < len(names) {
		return names[m]
	}
	return fmt.Sprintf("UNKNOWN(%d)", m)
}

// Option is a single TLV DHCP option.
type Option struct {
	Code byte
	Data []byte
}

// Options is an ordered list of DHCP options.
type Options []Option

// Get returns the data of the first option matching code, or nil.
func (opts Options) Get(code byte) []byte {
	for _, o := range opts {
		if o.Code == code {
			return o.Data
		}
	}
	return nil
}

// MessageType returns the DHCP message type option value.
func (opts Options) MessageType() MessageType {
	if d := opts.Get(OptMessageType); len(d) == 1 {
		return MessageType(d[0])
	}
	return 0
}

// ServerID returns the server identifier IP.
func (opts Options) ServerID() net.IP {
	if d := opts.Get(OptServerID); len(d) == 4 {
		ip := make(net.IP, 4)
		copy(ip, d)
		return ip
	}
	return nil
}

// SubnetMask returns the subnet mask.
func (opts Options) SubnetMask() net.IPMask {
	if d := opts.Get(OptSubnetMask); len(d) == 4 {
		m := make(net.IPMask, 4)
		copy(m, d)
		return m
	}
	return nil
}

// Router returns the first router (gateway) IP.
func (opts Options) Router() net.IP {
	if d := opts.Get(OptRouter); len(d) >= 4 {
		ip := make(net.IP, 4)
		copy(ip, d[:4])
		return ip
	}
	return nil
}

// DNS returns all DNS server IPs.
func (opts Options) DNS() []net.IP {
	d := opts.Get(OptDNS)
	var out []net.IP
	for i := 0; i+4 <= len(d); i += 4 {
		ip := make(net.IP, 4)
		copy(ip, d[i:i+4])
		out = append(out, ip)
	}
	return out
}

// LeaseTime returns the lease duration in seconds.
func (opts Options) LeaseTime() uint32 {
	if d := opts.Get(OptLeaseTime); len(d) == 4 {
		return binary.BigEndian.Uint32(d)
	}
	return 0
}

// DomainName returns the domain name string.
func (opts Options) DomainName() string {
	if d := opts.Get(OptDomainName); len(d) > 0 {
		return string(d)
	}
	return ""
}

// Marshal serializes options to wire format (without the End marker).
func (opts Options) Marshal() []byte {
	var buf []byte
	for _, o := range opts {
		if o.Code == OptPad || o.Code == OptEnd {
			continue
		}
		buf = append(buf, o.Code, byte(len(o.Data)))
		buf = append(buf, o.Data...)
	}
	return buf
}

// ParseOptions decodes DHCP options from raw bytes.
func ParseOptions(data []byte) (Options, error) {
	var opts Options
	for i := 0; i < len(data); {
		code := data[i]
		i++
		if code == OptPad {
			continue
		}
		if code == OptEnd {
			break
		}
		if i >= len(data) {
			return opts, fmt.Errorf("dhcp: option %d truncated (no length)", code)
		}
		n := int(data[i])
		i++
		if i+n > len(data) {
			return opts, fmt.Errorf("dhcp: option %d truncated (need %d, have %d)", code, n, len(data)-i)
		}
		d := make([]byte, n)
		copy(d, data[i:i+n])
		opts = append(opts, Option{Code: code, Data: d})
		i += n
	}
	return opts, nil
}

// Option builder helpers

func OptionMessageType(mt MessageType) Option {
	return Option{Code: OptMessageType, Data: []byte{byte(mt)}}
}

func OptionRequestedIP(ip net.IP) Option {
	return Option{Code: OptRequestedIP, Data: ipTo4(ip)}
}

func OptionServerID(ip net.IP) Option {
	return Option{Code: OptServerID, Data: ipTo4(ip)}
}

func OptionClientID(hwType byte, mac net.HardwareAddr) Option {
	d := make([]byte, 1+len(mac))
	d[0] = hwType
	copy(d[1:], mac)
	return Option{Code: OptClientID, Data: d}
}

func OptionParamRequestList(codes ...byte) Option {
	return Option{Code: OptParamRequestList, Data: codes}
}

func OptionHostname(name string) Option {
	return Option{Code: OptHostname, Data: []byte(name)}
}

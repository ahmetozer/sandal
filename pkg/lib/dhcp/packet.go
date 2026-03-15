package dhcp

import (
	"encoding/binary"
	"fmt"
	"net"
)

// DHCP op codes (RFC 2131)
const (
	OpRequest byte = 1
	OpReply   byte = 2
)

// Hardware types
const (
	HTypeEthernet byte = 1
)

// Flags
const (
	FlagBroadcast uint16 = 0x8000
)

const (
	magicCookie uint32 = 0x63825363
	headerSize         = 236 // fixed header before magic cookie
)

// Packet represents a DHCP packet (RFC 2131).
type Packet struct {
	Op     byte
	HType  byte
	HLen   byte
	Hops   byte
	XID    uint32
	Secs   uint16
	Flags  uint16
	CIAddr net.IP           // Client IP address
	YIAddr net.IP           // 'Your' (offered) IP address
	SIAddr net.IP           // Server IP address
	GIAddr net.IP           // Relay agent IP address
	CHAddr net.HardwareAddr // Client hardware address
	Options Options
}

// Marshal serializes the packet to wire format.
func (p *Packet) Marshal() []byte {
	buf := make([]byte, headerSize+4) // header + magic cookie
	buf[0] = p.Op
	buf[1] = p.HType
	buf[2] = p.HLen
	buf[3] = p.Hops
	binary.BigEndian.PutUint32(buf[4:8], p.XID)
	binary.BigEndian.PutUint16(buf[8:10], p.Secs)
	binary.BigEndian.PutUint16(buf[10:12], p.Flags)
	copy(buf[12:16], ipTo4(p.CIAddr))
	copy(buf[16:20], ipTo4(p.YIAddr))
	copy(buf[20:24], ipTo4(p.SIAddr))
	copy(buf[24:28], ipTo4(p.GIAddr))
	if len(p.CHAddr) > 0 {
		copy(buf[28:44], p.CHAddr) // chaddr field is 16 bytes
	}
	// sname (64 bytes at offset 44) and file (128 bytes at offset 108) left zeroed
	binary.BigEndian.PutUint32(buf[headerSize:headerSize+4], magicCookie)
	buf = append(buf, p.Options.Marshal()...)
	buf = append(buf, OptEnd)
	return buf
}

// Unmarshal parses a DHCP packet from wire format.
func Unmarshal(data []byte) (*Packet, error) {
	if len(data) < headerSize+4 {
		return nil, fmt.Errorf("dhcp: packet too short (%d bytes)", len(data))
	}
	cookie := binary.BigEndian.Uint32(data[headerSize : headerSize+4])
	if cookie != magicCookie {
		return nil, fmt.Errorf("dhcp: invalid magic cookie %#x", cookie)
	}
	p := &Packet{
		Op:    data[0],
		HType: data[1],
		HLen:  data[2],
		Hops:  data[3],
		XID:   binary.BigEndian.Uint32(data[4:8]),
		Secs:  binary.BigEndian.Uint16(data[8:10]),
		Flags: binary.BigEndian.Uint16(data[10:12]),
	}
	p.CIAddr = copyIP4(data[12:16])
	p.YIAddr = copyIP4(data[16:20])
	p.SIAddr = copyIP4(data[20:24])
	p.GIAddr = copyIP4(data[24:28])
	hlen := int(p.HLen)
	if hlen > 16 {
		hlen = 16
	}
	p.CHAddr = make(net.HardwareAddr, hlen)
	copy(p.CHAddr, data[28:28+hlen])
	var err error
	p.Options, err = ParseOptions(data[headerSize+4:])
	if err != nil {
		return nil, err
	}
	return p, nil
}

// ipTo4 returns a 4-byte IPv4 representation, defaulting to 0.0.0.0 for nil.
func ipTo4(ip net.IP) net.IP {
	if ip == nil {
		return net.IPv4zero.To4()
	}
	if v4 := ip.To4(); v4 != nil {
		return v4
	}
	return net.IPv4zero.To4()
}

func copyIP4(b []byte) net.IP {
	ip := make(net.IP, 4)
	copy(ip, b[:4])
	return ip
}

package dhcp

import (
	"encoding/binary"
	"fmt"
	"net"
)

// DHCPv6 option codes (RFC 8415, RFC 3646)
const (
	Opt6ClientID    uint16 = 1  // Client Identifier (DUID)
	Opt6ServerID    uint16 = 2  // Server Identifier (DUID)
	Opt6IANA        uint16 = 3  // Identity Association for Non-temporary Addresses
	Opt6IATA        uint16 = 4  // Identity Association for Temporary Addresses
	Opt6IAAddr      uint16 = 5  // IA Address
	Opt6ORO         uint16 = 6  // Option Request Option
	Opt6Preference  uint16 = 7  // Preference
	Opt6ElapsedTime uint16 = 8  // Elapsed Time
	Opt6StatusCode  uint16 = 13 // Status Code
	Opt6RapidCommit uint16 = 14 // Rapid Commit
	Opt6DNSServers  uint16 = 23 // DNS Recursive Name Server (RFC 3646)
	Opt6DomainList  uint16 = 24 // Domain Search List (RFC 3646)
	Opt6IAPD        uint16 = 25 // Identity Association for Prefix Delegation
	Opt6IAPrefix    uint16 = 26 // IA Prefix
)

// DUID types (RFC 8415 §11)
const (
	DUIDTypeLLT  uint16 = 1 // Link-layer address plus time
	DUIDTypeEN   uint16 = 2 // Enterprise number
	DUIDTypeLL   uint16 = 3 // Link-layer address
	DUIDTypeUUID uint16 = 4 // UUID-based
)

// DHCPv6 status codes (RFC 8415 §21.13)
const (
	Status6Success      uint16 = 0
	Status6UnspecFail   uint16 = 1
	Status6NoAddrsAvail uint16 = 2
	Status6NoBinding    uint16 = 3
	Status6NotOnLink    uint16 = 4
	Status6UseMulticast uint16 = 5
)

// Option6 is a single DHCPv6 TLV option (2-byte code + 2-byte length + data).
type Option6 struct {
	Code uint16
	Data []byte
}

// Options6 is an ordered list of DHCPv6 options.
type Options6 []Option6

// Get returns the data of the first option matching code, or nil.
func (opts Options6) Get(code uint16) []byte {
	for _, o := range opts {
		if o.Code == code {
			return o.Data
		}
	}
	return nil
}

// ClientID returns the client DUID.
func (opts Options6) ClientID() []byte {
	return opts.Get(Opt6ClientID)
}

// ServerID returns the server DUID.
func (opts Options6) ServerID() []byte {
	return opts.Get(Opt6ServerID)
}

// IANA parses the first IA_NA option into its components.
func (opts Options6) IANA() *IANA {
	d := opts.Get(Opt6IANA)
	if len(d) < 12 {
		return nil
	}
	ia := &IANA{
		IAID: binary.BigEndian.Uint32(d[0:4]),
		T1:   binary.BigEndian.Uint32(d[4:8]),
		T2:   binary.BigEndian.Uint32(d[8:12]),
	}
	if len(d) > 12 {
		ia.Options, _ = ParseOptions6(d[12:])
	}
	return ia
}

// DNS returns the DNS recursive name server addresses (option 23).
func (opts Options6) DNS() []net.IP {
	d := opts.Get(Opt6DNSServers)
	var out []net.IP
	for i := 0; i+16 <= len(d); i += 16 {
		ip := make(net.IP, 16)
		copy(ip, d[i:i+16])
		out = append(out, ip)
	}
	return out
}

// StatusCode returns the status code and message from a Status Code option.
func (opts Options6) StatusCode() (uint16, string) {
	d := opts.Get(Opt6StatusCode)
	if len(d) < 2 {
		return Status6Success, ""
	}
	code := binary.BigEndian.Uint16(d[0:2])
	msg := ""
	if len(d) > 2 {
		msg = string(d[2:])
	}
	return code, msg
}

// Marshal serializes DHCPv6 options to wire format.
func (opts Options6) Marshal() []byte {
	var buf []byte
	for _, o := range opts {
		hdr := make([]byte, 4)
		binary.BigEndian.PutUint16(hdr[0:2], o.Code)
		binary.BigEndian.PutUint16(hdr[2:4], uint16(len(o.Data)))
		buf = append(buf, hdr...)
		buf = append(buf, o.Data...)
	}
	return buf
}

// ParseOptions6 decodes DHCPv6 options from raw bytes.
func ParseOptions6(data []byte) (Options6, error) {
	var opts Options6
	for i := 0; i < len(data); {
		if i+4 > len(data) {
			return opts, fmt.Errorf("dhcp6: option header truncated at offset %d", i)
		}
		code := binary.BigEndian.Uint16(data[i : i+2])
		length := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		i += 4
		if i+length > len(data) {
			return opts, fmt.Errorf("dhcp6: option %d truncated (need %d, have %d)", code, length, len(data)-i)
		}
		d := make([]byte, length)
		copy(d, data[i:i+length])
		opts = append(opts, Option6{Code: code, Data: d})
		i += length
	}
	return opts, nil
}

// IANA represents an Identity Association for Non-temporary Addresses (option 3).
type IANA struct {
	IAID    uint32
	T1      uint32 // Renewal time (seconds)
	T2      uint32 // Rebinding time (seconds)
	Options Options6
}

// Addresses returns all IA_ADDR entries nested in this IA_NA.
func (ia *IANA) Addresses() []IAAddr {
	var addrs []IAAddr
	for _, o := range ia.Options {
		if o.Code == Opt6IAAddr && len(o.Data) >= 24 {
			addr := IAAddr{
				IP:                make(net.IP, 16),
				PreferredLifetime: binary.BigEndian.Uint32(o.Data[16:20]),
				ValidLifetime:     binary.BigEndian.Uint32(o.Data[20:24]),
			}
			copy(addr.IP, o.Data[0:16])
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

// Marshal serializes the IA_NA to wire format (the option data, not the TLV wrapper).
func (ia *IANA) Marshal() []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], ia.IAID)
	binary.BigEndian.PutUint32(buf[4:8], ia.T1)
	binary.BigEndian.PutUint32(buf[8:12], ia.T2)
	buf = append(buf, ia.Options.Marshal()...)
	return buf
}

// IAAddr represents a single IA Address (option 5 data).
type IAAddr struct {
	IP                net.IP
	PreferredLifetime uint32
	ValidLifetime     uint32
}

// Builder helpers

// DUIDLL builds a DUID-LL (type 3) from a hardware type and MAC address.
func DUIDLL(hwType uint16, mac net.HardwareAddr) []byte {
	d := make([]byte, 4+len(mac))
	binary.BigEndian.PutUint16(d[0:2], DUIDTypeLL)
	binary.BigEndian.PutUint16(d[2:4], hwType)
	copy(d[4:], mac)
	return d
}

func Option6ClientID(duid []byte) Option6 {
	d := make([]byte, len(duid))
	copy(d, duid)
	return Option6{Code: Opt6ClientID, Data: d}
}

func Option6ServerID(duid []byte) Option6 {
	d := make([]byte, len(duid))
	copy(d, duid)
	return Option6{Code: Opt6ServerID, Data: d}
}

func Option6IANAOpt(ia *IANA) Option6 {
	return Option6{Code: Opt6IANA, Data: ia.Marshal()}
}

func Option6IAAddr(ip net.IP, preferred, valid uint32) Option6 {
	d := make([]byte, 24)
	copy(d[0:16], ip.To16())
	binary.BigEndian.PutUint32(d[16:20], preferred)
	binary.BigEndian.PutUint32(d[20:24], valid)
	return Option6{Code: Opt6IAAddr, Data: d}
}

func Option6ORO(codes ...uint16) Option6 {
	d := make([]byte, len(codes)*2)
	for i, c := range codes {
		binary.BigEndian.PutUint16(d[i*2:(i+1)*2], c)
	}
	return Option6{Code: Opt6ORO, Data: d}
}

func Option6ElapsedTime(centiseconds uint16) Option6 {
	d := make([]byte, 2)
	binary.BigEndian.PutUint16(d, centiseconds)
	return Option6{Code: Opt6ElapsedTime, Data: d}
}

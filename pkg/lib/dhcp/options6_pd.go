package dhcp

import (
	"encoding/binary"
	"fmt"
	"net"
)

// IAPrefix represents an IA_PREFIX option (RFC 8415 §21.22).
//
// Wire format (option data after the 4-byte TLV header):
//
//	preferred-lifetime (4) | valid-lifetime (4) | prefix-length (1) | prefix (16) | options (variable)
type IAPrefix struct {
	PreferredLifetime uint32
	ValidLifetime     uint32
	PrefixLength      uint8
	Prefix            net.IP // always 16 bytes on the wire
	Options           Options6
}

// IAPD represents an Identity Association for Prefix Delegation (RFC 8415 §21.21).
type IAPD struct {
	IAID    uint32
	T1      uint32 // Renewal time (seconds)
	T2      uint32 // Rebinding time (seconds)
	Options Options6
}

// Prefixes returns all IA_PREFIX entries nested in this IA_PD.
func (ia *IAPD) Prefixes() []IAPrefix {
	var out []IAPrefix
	for _, o := range ia.Options {
		if o.Code != Opt6IAPrefix {
			continue
		}
		p, err := parseIAPrefix(o.Data)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Marshal serializes the IA_PD option data (without TLV wrapper).
func (ia *IAPD) Marshal() []byte {
	buf := make([]byte, 12)
	binary.BigEndian.PutUint32(buf[0:4], ia.IAID)
	binary.BigEndian.PutUint32(buf[4:8], ia.T1)
	binary.BigEndian.PutUint32(buf[8:12], ia.T2)
	buf = append(buf, ia.Options.Marshal()...)
	return buf
}

// IAPD parses the first IA_PD option in this list.
func (opts Options6) IAPD() *IAPD {
	d := opts.Get(Opt6IAPD)
	return parseIAPD(d)
}

func parseIAPD(d []byte) *IAPD {
	if len(d) < 12 {
		return nil
	}
	ia := &IAPD{
		IAID: binary.BigEndian.Uint32(d[0:4]),
		T1:   binary.BigEndian.Uint32(d[4:8]),
		T2:   binary.BigEndian.Uint32(d[8:12]),
	}
	if len(d) > 12 {
		ia.Options, _ = ParseOptions6(d[12:])
	}
	return ia
}

func parseIAPrefix(d []byte) (IAPrefix, error) {
	if len(d) < 25 {
		return IAPrefix{}, fmt.Errorf("dhcp6: IA_PREFIX option data < 25 bytes")
	}
	p := IAPrefix{
		PreferredLifetime: binary.BigEndian.Uint32(d[0:4]),
		ValidLifetime:     binary.BigEndian.Uint32(d[4:8]),
		PrefixLength:      d[8],
		Prefix:            make(net.IP, 16),
	}
	copy(p.Prefix, d[9:25])
	if len(d) > 25 {
		p.Options, _ = ParseOptions6(d[25:])
	}
	return p, nil
}

// Builders.

func Option6IAPDOpt(ia *IAPD) Option6 {
	return Option6{Code: Opt6IAPD, Data: ia.Marshal()}
}

func Option6IAPrefixOpt(p IAPrefix) Option6 {
	d := make([]byte, 25)
	binary.BigEndian.PutUint32(d[0:4], p.PreferredLifetime)
	binary.BigEndian.PutUint32(d[4:8], p.ValidLifetime)
	d[8] = p.PrefixLength
	copy(d[9:25], p.Prefix.To16())
	d = append(d, p.Options.Marshal()...)
	return Option6{Code: Opt6IAPrefix, Data: d}
}


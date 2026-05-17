// Minimal RA emitter for integration tests. Sends ICMPv6 Router Advertisements
// every 2 seconds with a configurable PrefixInformation option.
package main

import (
	"flag"
	"log"
	"net"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv6"
)

// rawBody implements icmp.MessageBody so we can hand a pre-built RA body to
// the icmp package without having to compute the checksum ourselves.
type rawBody []byte

func (r rawBody) Len(_ int) int                 { return len(r) }
func (r rawBody) Marshal(_ int) ([]byte, error) { return []byte(r), nil }

func main() {
	dev := flag.String("dev", "eth0", "interface to send RAs from")
	prefixStr := flag.String("prefix", "2001:db8::/64", "advertised prefix")
	valid := flag.Int("valid", 60, "valid lifetime seconds")
	flag.Parse()

	_, prefix, err := net.ParseCIDR(*prefixStr)
	if err != nil {
		log.Fatalf("parse prefix: %v", err)
	}
	ones, _ := prefix.Mask.Size()

	conn, err := icmp.ListenPacket("ip6:ipv6-icmp", "")
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	defer conn.Close()

	iface, err := net.InterfaceByName(*dev)
	if err != nil {
		log.Fatalf("iface %q: %v", *dev, err)
	}

	for {
		body := buildRABody(prefix.IP, uint8(ones), uint32(*valid))
		msg := icmp.Message{
			Type: ipv6.ICMPTypeRouterAdvertisement,
			Code: 0,
			Body: rawBody(body),
		}
		b, err := msg.Marshal(nil)
		if err != nil {
			log.Fatalf("marshal: %v", err)
		}
		dst := &net.UDPAddr{IP: net.ParseIP("ff02::1"), Zone: iface.Name}
		if _, err := conn.WriteTo(b, dst); err != nil {
			log.Printf("write: %v", err)
		}
		time.Sleep(2 * time.Second)
	}
}

// buildRABody returns the body bytes that follow the ICMPv6 header for a
// Router Advertisement with one Prefix Information option.
//
// RA header (12 bytes after ICMP TLV header):
//
//	cur-hop-limit (1) | flags (1) | router-lifetime (2) | reachable (4) | retrans (4)
//
// Prefix Information option (32 bytes, type 3, length 4x8):
//
//	type (1) | length-in-units-of-8 (1) | prefix-length (1) | flags (1)
//	valid-lifetime (4) | preferred-lifetime (4) | reserved (4) | prefix (16)
func buildRABody(prefix net.IP, prefixLen uint8, validLifetime uint32) []byte {
	body := make([]byte, 12+32)
	body[0] = 64                                 // cur hop limit
	body[2], body[3] = 0x07, 0x08               // router lifetime 1800s
	body[12] = 3                                 // option type: prefix info
	body[13] = 4                                 // length in 8-byte units
	body[14] = prefixLen                         // prefix length
	body[15] = 0xc0                              // flags: on-link + autonomous
	body[16] = byte(validLifetime >> 24)
	body[17] = byte(validLifetime >> 16)
	body[18] = byte(validLifetime >> 8)
	body[19] = byte(validLifetime)
	body[20] = byte(validLifetime >> 24)
	body[21] = byte(validLifetime >> 16)
	body[22] = byte(validLifetime >> 8)
	body[23] = byte(validLifetime)
	copy(body[28:44], prefix.To16())
	return body
}

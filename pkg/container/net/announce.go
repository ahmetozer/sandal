//go:build linux

package net

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// AnnounceIPv4 sends gratuitous ARP frames for ip out of iface so peers on
// the LAN overwrite any cached IP→MAC mapping pointing at a previous owner
// (e.g. an old container with the same address). The first frame is sent
// synchronously; remaining count-1 frames fire in a background goroutine
// spaced gap apart so the caller is not blocked.
//
// RFC 5227 ARP Announcement: opcode=request, sender IP == target IP, target
// MAC zeroed, destination broadcast.
func AnnounceIPv4(iface string, ip net.IP, count int, gap time.Duration) error {
	if count < 1 {
		count = 1
	}
	v4 := ip.To4()
	if v4 == nil {
		return fmt.Errorf("announce: %s is not IPv4", ip)
	}
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return fmt.Errorf("announce: link %q: %w", iface, err)
	}
	mac := link.Attrs().HardwareAddr
	if len(mac) != 6 {
		return fmt.Errorf("announce: iface %q has no 6-byte MAC", iface)
	}
	idx := link.Attrs().Index

	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_ARP)))
	if err != nil {
		return fmt.Errorf("announce: AF_PACKET socket: %w", err)
	}
	frame := buildGratuitousARP(mac, v4)
	sa := &unix.SockaddrLinklayer{
		Protocol: htons(unix.ETH_P_ARP),
		Ifindex:  idx,
		Halen:    6,
	}
	copy(sa.Addr[:6], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})

	if err := unix.Sendto(fd, frame, 0, sa); err != nil {
		unix.Close(fd)
		return fmt.Errorf("announce: sendto: %w", err)
	}
	if count == 1 {
		unix.Close(fd)
		return nil
	}
	go func() {
		defer unix.Close(fd)
		for i := 1; i < count; i++ {
			time.Sleep(gap)
			if err := unix.Sendto(fd, frame, 0, sa); err != nil {
				slog.Warn("announce: gratuitous ARP retransmit failed", "iface", iface, "ip", v4, "err", err)
				return
			}
		}
	}()
	return nil
}

// AnnounceIPv6 sends unsolicited Neighbor Advertisements for ip out of iface
// to the all-nodes multicast group (ff02::1) with the Override flag set, so
// peers update their neighbor cache for ip→MAC. Same sync/async split as
// AnnounceIPv4.
//
// RFC 4861 §4.4 / §7.2.6: NA with R=0, S=0, O=1, target=ip, with a Target
// Link-Layer Address option. Hop limit must be 255 (§7.1.2).
func AnnounceIPv6(iface string, ip net.IP, count int, gap time.Duration) error {
	if count < 1 {
		count = 1
	}
	v6 := ip.To16()
	if v6 == nil || ip.To4() != nil {
		return fmt.Errorf("announce: %s is not IPv6", ip)
	}
	link, err := netlink.LinkByName(iface)
	if err != nil {
		return fmt.Errorf("announce: link %q: %w", iface, err)
	}
	mac := link.Attrs().HardwareAddr
	if len(mac) != 6 {
		return fmt.Errorf("announce: iface %q has no 6-byte MAC", iface)
	}
	idx := link.Attrs().Index

	fd, err := unix.Socket(unix.AF_INET6, unix.SOCK_RAW, unix.IPPROTO_ICMPV6)
	if err != nil {
		return fmt.Errorf("announce: ICMPv6 raw socket: %w", err)
	}
	// RFC 4861: hop limit must be 255.
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_HOPS, 255); err != nil {
		unix.Close(fd)
		return fmt.Errorf("announce: IPV6_MULTICAST_HOPS: %w", err)
	}
	// Pin egress to the requested interface.
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_IF, idx); err != nil {
		unix.Close(fd)
		return fmt.Errorf("announce: IPV6_MULTICAST_IF: %w", err)
	}
	// Kernel computes ICMPv6 checksum; checksum field is at byte offset 2.
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_CHECKSUM, 2); err != nil {
		unix.Close(fd)
		return fmt.Errorf("announce: IPV6_CHECKSUM: %w", err)
	}

	payload := buildUnsolicitedNA(mac, v6)
	sa := &unix.SockaddrInet6{}
	// ff02::1 — all-nodes link-local multicast.
	sa.Addr = [16]byte{0xff, 0x02, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0x01}

	if err := unix.Sendto(fd, payload, 0, sa); err != nil {
		unix.Close(fd)
		return fmt.Errorf("announce: sendto: %w", err)
	}
	if count == 1 {
		unix.Close(fd)
		return nil
	}
	go func() {
		defer unix.Close(fd)
		for i := 1; i < count; i++ {
			time.Sleep(gap)
			if err := unix.Sendto(fd, payload, 0, sa); err != nil {
				slog.Warn("announce: unsolicited NA retransmit failed", "iface", iface, "ip", v6, "err", err)
				return
			}
		}
	}()
	return nil
}

// AnnounceLinkAddrs sends gratuitous announcements for every non-link-local
// address on the link. Called from container guest init after links are
// finalized; failures are logged but never returned (best-effort).
func AnnounceLinkAddrs(iface string, addrs Addrs) {
	const (
		count = 3
		gap   = 500 * time.Millisecond
	)
	_, ll, _ := net.ParseCIDR("fe80::/10")
	for _, a := range addrs {
		if a.IP == nil {
			continue
		}
		if a.IP.To4() != nil {
			if err := AnnounceIPv4(iface, a.IP, count, gap); err != nil {
				slog.Warn("announce: IPv4 failed", "iface", iface, "ip", a.IP, "err", err)
			}
			continue
		}
		if ll.Contains(a.IP) {
			continue
		}
		if err := AnnounceIPv6(iface, a.IP, count, gap); err != nil {
			slog.Warn("announce: IPv6 failed", "iface", iface, "ip", a.IP, "err", err)
		}
	}
}

// buildGratuitousARP returns a 42-byte Ethernet+ARP frame announcing ip from
// srcMAC to the broadcast address (RFC 5227 ARP Announcement).
func buildGratuitousARP(srcMAC net.HardwareAddr, ip net.IP) []byte {
	v4 := ip.To4()
	frame := make([]byte, 42)

	// Ethernet header (14 bytes).
	copy(frame[0:6], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}) // dst
	copy(frame[6:12], srcMAC)                                    // src
	binary.BigEndian.PutUint16(frame[12:14], 0x0806)             // ethertype = ARP

	// ARP payload (28 bytes).
	binary.BigEndian.PutUint16(frame[14:16], 0x0001) // HTYPE = Ethernet
	binary.BigEndian.PutUint16(frame[16:18], 0x0800) // PTYPE = IPv4
	frame[18] = 6                                    // HLEN
	frame[19] = 4                                    // PLEN
	binary.BigEndian.PutUint16(frame[20:22], 0x0001) // OPER = request (RFC 5227)
	copy(frame[22:28], srcMAC)                       // SHA
	copy(frame[28:32], v4)                           // SPA
	// THA at frame[32:38] left zero
	copy(frame[38:42], v4) // TPA == SPA (gratuitous)
	return frame
}

// buildUnsolicitedNA returns the 32-byte ICMPv6 payload for an unsolicited
// Neighbor Advertisement with Override=1 announcing ip→srcMAC. The IPv6
// header and ICMPv6 checksum are added by the kernel.
func buildUnsolicitedNA(srcMAC net.HardwareAddr, ip net.IP) []byte {
	v6 := ip.To16()
	p := make([]byte, 32)

	p[0] = 136 // type = Neighbor Advertisement
	p[1] = 0   // code
	// p[2:4] checksum — kernel fills via IPV6_CHECKSUM offset 2

	// Flags byte at p[4]: R=0 S=0 O=1, then 3 reserved bytes.
	p[4] = 0x20 // O = 1 (override)
	// p[5:8] reserved (zero)

	copy(p[8:24], v6) // target address

	// Option: Target Link-Layer Address (RFC 4861 §4.6.1).
	p[24] = 2 // type = Target Link-Layer Address
	p[25] = 1 // length in units of 8 bytes
	copy(p[26:32], srcMAC)
	return p
}

func htons(v uint16) uint16 {
	return (v<<8)&0xff00 | v>>8
}

// addrState is an IPv6 address's DAD state on a link, as far as announcing
// it is concerned.
type addrState int

const (
	addrMissing addrState = iota
	addrTentative
	addrDADFailed
	addrReady
)

func (s addrState) String() string {
	switch s {
	case addrTentative:
		return "tentative"
	case addrDADFailed:
		return "dad-failed"
	case addrReady:
		return "ready"
	default:
		return "missing"
	}
}

// ipv6AddrState reports the DAD state of ip in addrs.
func ipv6AddrState(addrs []netlink.Addr, ip net.IP) addrState {
	for _, a := range addrs {
		if !a.IP.Equal(ip) {
			continue
		}
		switch {
		case a.Flags&unix.IFA_F_DADFAILED != 0:
			return addrDADFailed
		case a.Flags&unix.IFA_F_TENTATIVE != 0:
			return addrTentative
		default:
			return addrReady
		}
	}
	return addrMissing
}

// linkLocalReady reports whether the link has a link-local address that has
// finished DAD; the kernel needs one as the source of an NA to ff02::1.
func linkLocalReady(addrs []netlink.Addr) bool {
	for _, a := range addrs {
		if a.IP.IsLinkLocalUnicast() && a.Flags&(unix.IFA_F_TENTATIVE|unix.IFA_F_DADFAILED) == 0 {
			return true
		}
	}
	return false
}

// waitIPv6Ready polls the link's addresses until the link-local and every
// target have left DAD, or timeout elapses. RFC 4862 §5.4 forbids
// announcing a tentative address, and the kernel cannot send to ff02::1
// before the link-local is usable (EADDRNOTAVAIL). Targets that are still
// tentative at the deadline, failed DAD, or are missing are returned in
// skipped with their state.
func waitIPv6Ready(list func() ([]netlink.Addr, error), targets []net.IP, timeout, poll time.Duration) (ready []net.IP, skipped map[string]addrState) {
	deadline := time.Now().Add(timeout)
	for {
		addrs, err := list()
		ready, skipped = nil, map[string]addrState{}
		pending := err == nil && !linkLocalReady(addrs)
		for _, ip := range targets {
			st := addrMissing
			if err == nil {
				st = ipv6AddrState(addrs, ip)
			}
			switch st {
			case addrReady:
				ready = append(ready, ip)
			case addrTentative:
				pending = true
				skipped[ip.String()] = st
			default:
				skipped[ip.String()] = st
			}
		}
		if !pending || time.Now().After(deadline) {
			if err == nil && !linkLocalReady(addrs) {
				for _, ip := range ready {
					skipped[ip.String()] = addrTentative
				}
				ready = nil
			}
			return ready, skipped
		}
		time.Sleep(poll)
	}
}

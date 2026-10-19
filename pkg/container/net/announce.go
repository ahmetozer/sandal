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

// announcement is one address's gratuitous ARP or unsolicited NA sender.
type announcement struct {
	ip  net.IP
	fd  int
	msg []byte
	sa  unix.Sockaddr
}

func (a *announcement) send() error { return unix.Sendto(a.fd, a.msg, 0, a.sa) }
func (a *announcement) close()      { unix.Close(a.fd) }

// newGARP prepares an RFC 5227 ARP Announcement (opcode request, sender IP ==
// target IP, broadcast) for ip on link.
func newGARP(link netlink.Link, mac net.HardwareAddr, ip net.IP) (*announcement, error) {
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_ARP)))
	if err != nil {
		return nil, fmt.Errorf("AF_PACKET socket: %w", err)
	}
	sa := &unix.SockaddrLinklayer{Protocol: htons(unix.ETH_P_ARP), Ifindex: link.Attrs().Index, Halen: 6}
	copy(sa.Addr[:6], []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	return &announcement{ip: ip, fd: fd, msg: buildGratuitousARP(mac, ip.To4()), sa: sa}, nil
}

// newNA prepares an RFC 4861 unsolicited Neighbor Advertisement (R=0 S=0
// O=1, Target Link-Layer Address option) for ip to ff02::1 with hop limit
// 255. The kernel always computes the ICMPv6 checksum on ICMPv6 raw sockets;
// setting IPV6_CHECKSUM there fails with EINVAL (RFC 3542 §3.1).
func newNA(link netlink.Link, mac net.HardwareAddr, ip net.IP) (*announcement, error) {
	fd, err := unix.Socket(unix.AF_INET6, unix.SOCK_RAW, unix.IPPROTO_ICMPV6)
	if err != nil {
		return nil, fmt.Errorf("ICMPv6 raw socket: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_HOPS, 255); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("IPV6_MULTICAST_HOPS: %w", err)
	}
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_IF, link.Attrs().Index); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("IPV6_MULTICAST_IF: %w", err)
	}
	sa := &unix.SockaddrInet6{Addr: [16]byte{0xff, 0x02, 15: 0x01}}
	return &announcement{ip: ip, fd: fd, msg: buildUnsolicitedNA(mac, ip.To16()), sa: sa}, nil
}

// AnnounceLinkAddrs sends gratuitous ARP / unsolicited NA for every
// non-link-local address on the link so peers overwrite neighbor entries
// that still point at a previous container's MAC. It is synchronous on
// purpose: container init ends in exec, which kills any goroutine, so
// background retransmits never ran. IPv6 addresses are announced only after
// DAD (waiting at most dadWait); all addresses go out together in count
// rounds, gap apart. Best-effort: failures are logged, never returned.
func AnnounceLinkAddrs(iface string, addrs Addrs) {
	const (
		count   = 3
		gap     = 100 * time.Millisecond
		dadWait = 3 * time.Second
		dadPoll = 50 * time.Millisecond
	)
	link, err := netlink.LinkByName(iface)
	if err != nil {
		slog.Warn("announce: link lookup failed", "iface", iface, "err", err)
		return
	}
	mac := link.Attrs().HardwareAddr
	if len(mac) != 6 {
		slog.Debug("announce: no 6-byte MAC, skipping", "iface", iface)
		return
	}

	var v4, v6 []net.IP
	for _, a := range addrs {
		switch {
		case a.IP == nil, a.IP.IsLinkLocalUnicast():
		case a.IP.To4() != nil:
			v4 = append(v4, a.IP)
		default:
			v6 = append(v6, a.IP)
		}
	}

	var anns []*announcement
	defer func() {
		for _, a := range anns {
			if a != nil {
				a.close()
			}
		}
	}()
	for _, ip := range v4 {
		a, err := newGARP(link, mac, ip)
		if err != nil {
			slog.Warn("announce: IPv4 failed", "iface", iface, "ip", ip, "err", err)
			continue
		}
		anns = append(anns, a)
	}
	if len(v6) > 0 {
		list := func() ([]netlink.Addr, error) { return netlink.AddrList(link, netlink.FAMILY_V6) }
		ready, skipped := waitIPv6Ready(list, v6, dadWait, dadPoll)
		for ip, st := range skipped {
			slog.Warn("announce: IPv6 skipped", "iface", iface, "ip", ip, "state", st.String())
		}
		for _, ip := range ready {
			a, err := newNA(link, mac, ip)
			if err != nil {
				slog.Warn("announce: IPv6 failed", "iface", iface, "ip", ip, "err", err)
				continue
			}
			anns = append(anns, a)
		}
	}

	if len(anns) == 0 {
		return
	}
	for round := 0; round < count; round++ {
		for i, a := range anns {
			if a == nil {
				continue
			}
			if err := a.send(); err != nil {
				slog.Warn("announce: send failed", "iface", iface, "ip", a.ip, "err", err)
				a.close()
				anns[i] = nil
			}
		}
		if round < count-1 {
			time.Sleep(gap)
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
	// p[2:4] checksum — always computed by the kernel for ICMPv6 raw sockets

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

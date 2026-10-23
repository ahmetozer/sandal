//go:build linux

package renumber

import (
	"context"
	"encoding/binary"
	"errors"
	"log/slog"
	"net"
	"slices"
	"strings"
	"time"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// A router that renumbers (reboot, PPPoE reconnect) often forgets its old
// prefix instead of advertising it with lifetime 0. The kernel then keeps the
// old on-link route and SLAAC address until their lifetime runs out, and
// Select would stay on that dead prefix. Reading copies of the Router
// Advertisements on the uplink tells which prefixes the routers still
// advertise; a prefix they stopped advertising while advertising another one
// is "abandoned" and skipped by the selection. Only positive evidence counts:
// every router that advertised the prefix must since have sent an RA without
// it, and a Router Solicitation must have had time to collect the answers of
// all routers, so a router that split its PIOs over several RAs or went
// silent does not cause a (sticky) switch.
const (
	raMissLimit    = 2                      // consecutive RAs without the prefix, per router
	raMissTimeout  = 30 * time.Second       // or this long since an RA without it
	raSolicitGap   = 10 * time.Second       // at most one Router Solicitation per gap
	raConfirmDelay = 4 * time.Second        // wait this long for RS answers (MAX_RA_DELAY_TIME is 0.5 s)
	raBurstGap     = 200 * time.Millisecond // a router's RAs closer than this are one advertisement
	raWithdrawKeep = 2 * time.Hour          // kernel keeps a withdrawn SLAAC address up to 2h (RFC 4862 5.5.3e)
	raMaxRouters   = 8
	raMaxPrefixes  = 32
	raRetry        = 30 * time.Second // socket reopen delay after an error

	raReasonWithdrawn = "withdrawn (valid lifetime 0)"
	raReasonMissing   = "missing from 2 consecutive RAs"
	raReasonTimeout   = "not advertised for 30s"
)

// raPIO is one global /64 Prefix Information Option of a Router Advertisement.
type raPIO struct {
	Prefix *net.IPNet
	Valid  uint32 // seconds; 0 = withdrawn, 0xffffffff = infinite
}

// parseRA returns the global /64 PIOs with the L or A flag of an ICMPv6
// Router Advertisement (no IPv6 header). ULA, link-local and other prefix
// lengths are dropped, and so is a PIO the kernel ignores (too short,
// preferred > valid lifetime). ok is false for anything that is not a
// well-formed RA (RFC 4861 6.1.2: zero-length options discard the message).
func parseRA(b []byte) (pios []raPIO, ok bool) {
	if len(b) < 16 || b[0] != 134 || b[1] != 0 {
		return nil, false
	}
	for o := b[16:]; len(o) > 0; {
		if len(o) < 2 {
			return nil, false
		}
		l := int(o[1]) * 8
		if l == 0 || l > len(o) {
			return nil, false
		}
		if o[0] == 3 && l >= 32 {
			plen, flags := o[2], o[3]
			valid, pref := binary.BigEndian.Uint32(o[4:8]), binary.BigEndian.Uint32(o[8:12])
			ip := net.IP(slices.Clone(o[16:32]))
			if plen == 64 && flags&0xc0 != 0 && pref <= valid && isGlobal(ip) && !ip.IsMulticast() {
				m := net.CIDRMask(64, 128)
				pios = append(pios, raPIO{Prefix: &net.IPNet{IP: ip.Mask(m), Mask: m}, Valid: valid})
			}
		}
		o = o[l:]
	}
	return pios, true
}

// raHold is one router's advertisement of one prefix.
type raHold struct {
	misses   int       // consecutive RAs (with global PIOs) from the router without the prefix
	lastMiss time.Time // start of the burst of the last of those RAs
	until    time.Time // advertised valid lifetime end
}

// raPrefix is what all routers together said about one prefix.
type raPrefix struct {
	withdrawn bool
	missCount int       // RAs from any router without the prefix since it was last advertised
	missSince time.Time // first of those RAs
	rsAt      time.Time // first Router Solicitation sent since missSince
	until     time.Time // forget the prefix after this
}

// raTracker records which global prefixes each router (by RA source address)
// advertised recently. It is pure: callers pass the clock.
type raTracker struct {
	routers  map[string]map[string]*raHold
	prefixes map[string]*raPrefix
	bursts   map[string]*raBurst
	lastRS   time.Time // last Router Solicitation attempt (rate limit)
}

// raBurst is a router's current run of back-to-back RAs (PIOs split over
// several RAs) to the same kind of destination. Evidence is dated by its
// start: the tail of the burst that triggered an RS is not an answer to it,
// but a unicast RA, which only answers a solicitation, starts a new burst.
type raBurst struct {
	start, last time.Time
	unicast     bool
}

func newRATracker() *raTracker {
	t := &raTracker{}
	t.reset()
	return t
}

// reset forgets everything, so abandoned reports nothing (netlink-only
// behaviour) until RAs are seen again.
func (t *raTracker) reset() {
	t.routers = map[string]map[string]*raHold{}
	t.prefixes = map[string]*raPrefix{}
	t.bursts = map[string]*raBurst{}
	t.lastRS = time.Time{}
}

func raLifetimeEnd(now time.Time, valid uint32) time.Time {
	if valid == 0xffffffff {
		return now.Add(100 * 365 * 24 * time.Hour)
	}
	return now.Add(time.Duration(valid) * time.Second)
}

func laterOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// observe records one RA from router, sent to us (unicast) or to all nodes.
// RAs without any global PIO (e.g. only
// RDNSS) say nothing about prefixes and only apply withdrawals. It returns
// true when a Router Solicitation should be sent now: the RA lacks the active
// prefix and either brings a prefix not seen before or no RS was sent since
// the active prefix went missing, and none was attempted in the last
// raSolicitGap. The answers confirm quickly whether the active prefix is
// still advertised; the caller reports a sent RS with solicited.
func (t *raTracker) observe(router net.IP, unicast bool, pios []raPIO, active *net.IPNet, now time.Time) bool {
	t.expire(now)
	rkey := router.String()
	seen := map[string]bool{}
	for _, p := range pios {
		k := p.Prefix.String()
		if p.Valid > 0 {
			seen[k] = true
			continue
		}
		delete(t.routers[rkey], k)
		if st := t.prefixes[k]; st != nil {
			st.withdrawn = true
			st.until = laterOf(st.until, now.Add(raWithdrawKeep))
		}
	}
	if len(t.routers[rkey]) == 0 {
		delete(t.routers, rkey)
	}
	if len(seen) == 0 {
		return false
	}
	holds := t.routers[rkey]
	if holds == nil {
		if len(t.routers) >= raMaxRouters {
			return false
		}
		holds = map[string]*raHold{}
		t.routers[rkey] = holds
	}
	b := t.bursts[rkey]
	if b == nil || now.Sub(b.last) > raBurstGap || b.unicast != unicast {
		b = &raBurst{start: now, unicast: unicast}
		t.bursts[rkey] = b
	}
	b.last = now
	for k, h := range holds {
		if !seen[k] {
			h.misses++
			h.lastMiss = b.start
		}
	}
	activeKey := ""
	if active != nil {
		activeKey = active.String()
	}
	for k, st := range t.prefixes {
		if seen[k] {
			continue
		}
		if st.missCount == 0 {
			st.missSince = now
		}
		st.missCount++
	}
	activeUnconfirmed := false
	if st := t.prefixes[activeKey]; st != nil && !seen[activeKey] {
		activeUnconfirmed = st.rsAt.IsZero()
	}
	newPrefix := false
	for _, p := range pios {
		k := p.Prefix.String()
		if p.Valid == 0 {
			continue
		}
		st := t.prefixes[k]
		if st == nil {
			if len(t.prefixes) >= raMaxPrefixes {
				continue
			}
			st = &raPrefix{}
			t.prefixes[k] = st
			newPrefix = true
		}
		until := raLifetimeEnd(now, p.Valid)
		*st = raPrefix{until: laterOf(st.until, until)}
		holds[k] = &raHold{until: until}
	}
	if activeKey == "" || seen[activeKey] || (!newPrefix && !activeUnconfirmed) {
		return false
	}
	if !t.lastRS.IsZero() && now.Sub(t.lastRS) < raSolicitGap {
		return false
	}
	t.lastRS = now
	return true
}

// solicited records that a Router Solicitation was sent: the answers to it
// confirm every prefix that is missing now.
func (t *raTracker) solicited(now time.Time) {
	for _, st := range t.prefixes {
		if st.missCount > 0 && st.rsAt.IsZero() {
			st.rsAt = now
		}
	}
}

// expire drops holds and prefixes whose lifetime ended; the kernel has
// dropped them by then as well.
func (t *raTracker) expire(now time.Time) {
	for r, holds := range t.routers {
		for k, h := range holds {
			if now.After(h.until) {
				delete(holds, k)
			}
		}
		if len(holds) == 0 {
			delete(t.routers, r)
		}
	}
	for k, st := range t.prefixes {
		if now.After(st.until) {
			delete(t.prefixes, k)
		}
	}
	for r := range t.bursts {
		if t.routers[r] == nil {
			delete(t.bursts, r)
		}
	}
}

// abandoned returns the prefixes (as "prefix/64") that the routers stopped
// advertising, with the reason. A prefix is only abandoned while another
// global prefix is still advertised, and only when it was advertised before:
// without any RA the result is empty. Reasons:
//   - withdrawn: an RA carried it with valid lifetime 0 and no router still
//     advertises it;
//   - missing: every router that advertised it sent raMissLimit RAs in a row
//     without it, the last one in a burst that began after an RS sent since
//     it went missing, and that RS is raConfirmDelay old (all answers are in);
//   - timeout: at least raMissLimit RAs and raMissTimeout passed since the
//     first RA without it, and every router that advertised it sent a burst
//     without it since then (a silent router is no evidence; its prefix
//     stays until its lifetime ends, as in the kernel).
func (t *raTracker) abandoned(now time.Time) map[string]string {
	t.expire(now)
	out := map[string]string{}
	for k, st := range t.prefixes {
		if !t.advertisedOtherThan(k) {
			continue
		}
		holders, allMissed, answered, allHeard := 0, true, true, true
		for _, holds := range t.routers {
			if h := holds[k]; h != nil {
				holders++
				allMissed = allMissed && h.misses >= raMissLimit
				answered = answered && h.lastMiss.After(st.rsAt)
				allHeard = allHeard && !h.lastMiss.Before(st.missSince)
			}
		}
		confirmed := !st.rsAt.IsZero() && answered && now.Sub(st.rsAt) >= raConfirmDelay
		switch {
		case st.withdrawn && holders == 0:
			out[k] = raReasonWithdrawn
		case st.missCount >= raMissLimit && allMissed && confirmed:
			out[k] = raReasonMissing
		case st.missCount >= raMissLimit && allHeard && now.Sub(st.missSince) >= raMissTimeout:
			out[k] = raReasonTimeout
		}
	}
	return out
}

// advertisedOtherThan reports whether some router currently advertises a
// prefix other than k.
func (t *raTracker) advertisedOtherThan(k string) bool {
	for _, holds := range t.routers {
		for k2, h := range holds {
			if k2 != k && h.misses < raMissLimit {
				return true
			}
		}
	}
	return false
}

// dropAbandoned returns cands without the abandoned prefixes.
func dropAbandoned(cands []Candidate, abandoned map[string]string) []Candidate {
	if len(abandoned) == 0 {
		return cands
	}
	var out []Candidate
	for _, c := range cands {
		if c.Prefix != nil {
			if _, ok := abandoned[(&net.IPNet{IP: c.Prefix.IP.Mask(c.Prefix.Mask), Mask: c.Prefix.Mask}).String()]; ok {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// openRASocket opens a raw ICMPv6 socket that receives a copy of every
// Router Advertisement (the kernel still processes them itself), with the
// arrival interface and hop limit, and that can send Router Solicitations.
// It is not bound to the uplink: the arrival ifindex is compared with the
// uplink resolved by name per RA, so a recreated or renamed uplink is
// followed.
func openRASocket() (int, error) {
	fd, err := unix.Socket(unix.AF_INET6, unix.SOCK_RAW|unix.SOCK_CLOEXEC, unix.IPPROTO_ICMPV6)
	if err != nil {
		return -1, err
	}
	var f unix.ICMPv6Filter // a set bit blocks that type
	for i := range f.Data {
		f.Data[i] = 0xffffffff
	}
	f.Data[134>>5] &^= 1 << (134 & 31)
	tv := unix.NsecToTimeval(time.Second.Nanoseconds())
	for _, err := range []error{
		unix.SetsockoptICMPv6Filter(fd, unix.IPPROTO_ICMPV6, unix.ICMPV6_FILTER, &f),
		unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_RECVPKTINFO, 1),
		unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_RECVHOPLIMIT, 1),
		unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_HOPS, 255),
		// Wake up regularly so the 30 s timeout is noticed without new RAs.
		unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv),
	} {
		if err != nil {
			unix.Close(fd)
			return -1, err
		}
	}
	return fd, nil
}

// raControl extracts the arrival ifindex, hop limit and destination address
// from recvmsg control data; zero when missing.
func raControl(oob []byte) (ifindex, hops int, dst net.IP) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return 0, 0, nil
	}
	for _, m := range msgs {
		if m.Header.Level != unix.IPPROTO_IPV6 {
			continue
		}
		switch {
		case m.Header.Type == unix.IPV6_PKTINFO && len(m.Data) >= unix.SizeofInet6Pktinfo:
			dst = net.IP(slices.Clone(m.Data[:16]))
			ifindex = int(binary.NativeEndian.Uint32(m.Data[16:20]))
		case m.Header.Type == unix.IPV6_HOPLIMIT && len(m.Data) >= 4:
			hops = int(binary.NativeEndian.Uint32(m.Data[:4]))
		}
	}
	return ifindex, hops, dst
}

// sendRS sends a Router Solicitation to all-routers on ifindex. The kernel
// fills in the source address and the ICMPv6 checksum.
func sendRS(fd, ifindex int) error {
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_MULTICAST_IF, ifindex); err != nil {
		return err
	}
	return unix.Sendto(fd, []byte{133, 0, 0, 0, 0, 0, 0, 0}, 0, &unix.SockaddrInet6{Addr: [16]byte{0xff, 0x02, 15: 0x02}})
}

// watchRAs feeds the uplink's Router Advertisements into s.ra until ctx
// ends. On a socket error it logs, forgets what it learned (back to the
// netlink-only behaviour) and retries later.
func (s *RASource) watchRAs(ctx context.Context) {
	for {
		fd, err := openRASocket()
		if err == nil {
			err = s.readRAs(ctx, fd)
			unix.Close(fd)
		}
		if ctx.Err() != nil {
			return
		}
		slog.Warn("ra: router advertisement listener failed, using kernel prefixes only", "iface", s.UpstreamIf, "err", err, "retry_in", raRetry)
		s.mu.Lock()
		s.ra.reset()
		s.mu.Unlock()
		s.notifyIfChanged()
		select {
		case <-ctx.Done():
			return
		case <-time.After(raRetry):
		}
	}
}

// readRAs handles RAs from fd until ctx ends (nil) or the socket fails.
func (s *RASource) readRAs(ctx context.Context, fd int) error {
	buf := make([]byte, 65535)
	oob := make([]byte, 128)
	for ctx.Err() == nil {
		n, oobn, flags, from, err := unix.Recvmsg(fd, buf, oob, 0)
		switch {
		case errors.Is(err, unix.EAGAIN), errors.Is(err, unix.EINTR):
		case err != nil:
			return err
		case flags&(unix.MSG_TRUNC|unix.MSG_CTRUNC) == 0:
			s.handleRA(fd, buf[:n], oob[:oobn], from)
		}
		s.notifyIfChanged()
	}
	return nil
}

// handleRA validates one received RA (RFC 4861 6.1.2: hop limit 255,
// link-local source, arrived on the uplink) and records it.
func (s *RASource) handleRA(fd int, b, oob []byte, from unix.Sockaddr) {
	ifindex, hops, dst := raControl(oob)
	sa, ok := from.(*unix.SockaddrInet6)
	if !ok || hops != 255 {
		return
	}
	src := net.IP(slices.Clone(sa.Addr[:]))
	if !src.IsLinkLocalUnicast() {
		return
	}
	link, err := netlink.LinkByName(s.UpstreamIf)
	if err != nil || link.Attrs().Index != ifindex {
		return
	}
	pios, ok := parseRA(b)
	if !ok {
		return
	}
	s.mu.Lock()
	unicast := dst != nil && !dst.IsMulticast()
	solicit := s.ra.observe(src, unicast, pios, s.current, time.Now())
	s.mu.Unlock()
	if !solicit {
		return
	}
	slog.Debug("ra: active prefix missing from an RA, soliciting", "iface", s.UpstreamIf, "router", src)
	if err := sendRS(fd, ifindex); err != nil {
		slog.Warn("ra: router solicitation failed", "iface", s.UpstreamIf, "err", err)
		return
	}
	s.mu.Lock()
	s.ra.solicited(time.Now())
	s.mu.Unlock()
}

// notifyIfChanged wakes the source's main loop when the abandoned set
// changed, so it re-selects.
func (s *RASource) notifyIfChanged() {
	s.mu.Lock()
	ab := s.ra.abandoned(time.Now())
	s.mu.Unlock()
	keys := make([]string, 0, len(ab))
	for k := range ab {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	key := strings.Join(keys, ",")
	if key == s.raLastKey {
		return
	}
	s.raLastKey = key
	select {
	case s.raChanged <- struct{}{}:
	default:
	}
}

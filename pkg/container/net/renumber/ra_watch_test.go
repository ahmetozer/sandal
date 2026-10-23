//go:build linux

package renumber

import (
	"context"
	"encoding/binary"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// pioOpt builds a 32-byte Prefix Information Option.
func pioOpt(prefix string, plen, flags byte, valid uint32) []byte {
	o := make([]byte, 32)
	o[0], o[1], o[2], o[3] = 3, 4, plen, flags
	binary.BigEndian.PutUint32(o[4:8], valid)
	binary.BigEndian.PutUint32(o[8:12], valid)
	copy(o[16:32], net.ParseIP(prefix).To16())
	return o
}

// buildRA builds an ICMPv6 Router Advertisement (no IPv6 header) with opts.
func buildRA(opts ...[]byte) []byte {
	b := make([]byte, 16)
	b[0] = 134
	b[4] = 64
	binary.BigEndian.PutUint16(b[6:8], 1800)
	for _, o := range opts {
		b = append(b, o...)
	}
	return b
}

const (
	pioL = 0x80
	pioA = 0x40
)

func TestParseRA(t *testing.T) {
	slla := []byte{1, 1, 0x02, 0, 0, 0, 0, 1}
	mtu := []byte{5, 1, 0, 0, 0, 0, 0x05, 0xdc}
	ra := buildRA(
		slla,
		pioOpt("2001:db8:1:2::", 64, pioL, 300),             // L only (DHCPv6 network) -> kept
		pioOpt("2001:db8:3:4::", 64, pioA, 600),             // A only -> kept
		pioOpt("2001:db8:5:6::", 64, pioL|pioA, 0),          // withdrawn -> kept, Valid 0
		pioOpt("2001:db8:7:8::", 64, 0, 300),                // neither L nor A -> ignored
		pioOpt("fd00:db8:5::", 64, pioL|pioA, 300),          // ULA -> ignored
		pioOpt("fe80::", 64, pioL, 300),                     // link-local -> ignored
		pioOpt("2001:db8:9::", 56, pioL, 300),               // not /64 -> ignored
		pioOpt("2001:db8:a:b:ffff::", 64, pioL, 0xffffffff), // host bits masked, infinite
		mtu,
	)
	got, ok := parseRA(ra)
	if !ok {
		t.Fatal("parseRA rejected a valid RA")
	}
	want := []struct {
		prefix string
		valid  uint32
	}{
		{"2001:db8:1:2::/64", 300},
		{"2001:db8:3:4::/64", 600},
		{"2001:db8:5:6::/64", 0},
		{"2001:db8:a:b::/64", 0xffffffff},
	}
	if len(got) != len(want) {
		t.Fatalf("parseRA = %v, want %d PIOs", got, len(want))
	}
	for i, w := range want {
		if got[i].Prefix.String() != w.prefix || got[i].Valid != w.valid {
			t.Errorf("PIO %d = %s valid %d, want %s valid %d", i, got[i].Prefix, got[i].Valid, w.prefix, w.valid)
		}
	}
}

func TestParseRARejectsMalformed(t *testing.T) {
	good := pioOpt("2001:db8:1:2::", 64, pioL, 300)
	cases := map[string][]byte{
		"short":         buildRA()[:15],
		"not an RA":     append([]byte{133, 0, 0, 0, 0, 0, 0, 0}, make([]byte, 8)...),
		"nonzero code":  func() []byte { b := buildRA(good); b[1] = 1; return b }(),
		"zero length":   buildRA(good, []byte{1, 0, 0, 0, 0, 0, 0, 0}),
		"truncated opt": buildRA(good[:24]),
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if pios, ok := parseRA(b); ok {
				t.Fatalf("parseRA accepted malformed RA: %v", pios)
			}
		})
	}
	// Like the kernel, a too-short PIO is skipped, not the whole RA.
	short := pioOpt("2001:db8:1:2::", 64, pioL, 300)
	short[1] = 3
	if pios, ok := parseRA(buildRA(short[:24], good)); !ok || len(pios) != 1 || pios[0].Prefix.String() != "2001:db8:1:2::/64" {
		t.Fatalf("parseRA(short PIO + good PIO) = %v, %v; want only the good PIO", pios, ok)
	}
}

// raClock drives an raTracker with a fake clock.
type raClock struct {
	t   *testing.T
	tr  *raTracker
	now time.Time
}

func newRAClock(t *testing.T) *raClock {
	return &raClock{t: t, tr: newRATracker(), now: time.Unix(1_700_000_000, 0)}
}

func (c *raClock) advance(d time.Duration) { c.now = c.now.Add(d) }

// ra feeds one RA from router (a link-local address) carrying pios; active
// is the selected prefix ("" for none). It returns whether a Router
// Solicitation must be sent.
func (c *raClock) ra(router string, active string, pios ...raPIO) bool {
	c.t.Helper()
	var act *net.IPNet
	if active != "" {
		act = mustCIDR(c.t, active)
	}
	return c.tr.observe(net.ParseIP(router), false, pios, act, c.now)
}

// uni feeds one RA unicast to us: an answer to a Router Solicitation.
func (c *raClock) uni(router string, active string, pios ...raPIO) bool {
	c.t.Helper()
	return c.tr.observe(net.ParseIP(router), true, pios, mustCIDR(c.t, active), c.now)
}

// solicited records a Router Solicitation sent now.
func (c *raClock) solicited() { c.tr.solicited(c.now) }

func (c *raClock) abandoned(prefix string) (string, bool) {
	r, ok := c.tr.abandoned(c.now)[prefix]
	return r, ok
}

func adv(t *testing.T, prefix string) raPIO {
	return raPIO{Prefix: mustCIDR(t, prefix), Valid: 300}
}

func withdraw(t *testing.T, prefix string) raPIO {
	return raPIO{Prefix: mustCIDR(t, prefix), Valid: 0}
}

const (
	raP1 = "2001:db8:aaaa:1::/64"
	raP2 = "2001:db8:bbbb:1::/64"
	raP3 = "2001:db8:cccc:1::/64"
	rtr1 = "fe80::1"
	rtr2 = "fe80::2"
)

// abandonP1 makes rtr1 renumber from P1 to P2: the RA without P1 solicits,
// the router answers without P1, and the solicitation ages past
// raConfirmDelay.
func abandonP1(t *testing.T, c *raClock) {
	t.Helper()
	c.ra(rtr1, raP1, adv(t, raP1))
	c.advance(3 * time.Second)
	if !c.ra(rtr1, raP1, adv(t, raP2)) {
		t.Fatal("no solicitation on the first RA without the active prefix")
	}
	c.solicited()
	c.advance(300 * time.Millisecond)
	c.ra(rtr1, raP1, adv(t, raP2))
	c.advance(raConfirmDelay)
	if _, ok := c.abandoned(raP1); !ok {
		t.Fatal("precondition: P1 should be abandoned")
	}
}

func TestRATrackerAbandonsAfterSolicitedRAsWithoutPrefix(t *testing.T) {
	c := newRAClock(t)
	c.ra(rtr1, raP1, adv(t, raP1))
	c.advance(5 * time.Second)
	if !c.ra(rtr1, raP1, adv(t, raP2)) {
		t.Fatal("no solicitation on the first RA without the active prefix")
	}
	c.solicited()
	if _, ok := c.abandoned(raP1); ok {
		t.Fatal("P1 abandoned on a single RA without it")
	}
	c.advance(300 * time.Millisecond)
	c.ra(rtr1, raP1, adv(t, raP2)) // the answer to the RS
	if r, ok := c.abandoned(raP1); ok {
		t.Fatalf("P1 abandoned (%s) before the solicitation is %v old", r, raConfirmDelay)
	}
	c.advance(raConfirmDelay - 300*time.Millisecond - time.Millisecond)
	if r, ok := c.abandoned(raP1); ok {
		t.Fatalf("P1 abandoned (%s) before the solicitation is %v old", r, raConfirmDelay)
	}
	c.advance(time.Millisecond)
	reason, ok := c.abandoned(raP1)
	if !ok {
		t.Fatal("P1 not abandoned after a solicitation answered without it")
	}
	if reason != raReasonMissing {
		t.Fatalf("reason = %q, want %q", reason, raReasonMissing)
	}
	if _, ok := c.abandoned(raP2); ok {
		t.Fatal("P2 (still advertised) abandoned")
	}
}

func TestRATrackerUnicastAnswer(t *testing.T) {
	t.Run("without P1", func(t *testing.T) {
		// Routers answer an RS by unicast right away, within the gap that
		// joins a burst: the change of destination marks the answer.
		c := newRAClock(t)
		c.ra(rtr1, raP1, adv(t, raP1))
		c.advance(3 * time.Second)
		c.ra(rtr1, raP1, adv(t, raP2))
		c.solicited()
		c.advance(time.Millisecond)
		c.uni(rtr1, raP1, adv(t, raP2))
		c.advance(raConfirmDelay)
		if r, ok := c.abandoned(raP1); !ok || r != raReasonMissing {
			t.Fatalf("abandoned = %q, %v; want %q after a unicast answer without P1", r, ok, raReasonMissing)
		}
	})
	t.Run("split answer with P1", func(t *testing.T) {
		c := newRAClock(t)
		c.ra(rtr1, raP1, adv(t, raP1))
		c.advance(3 * time.Second)
		c.ra(rtr1, raP1, adv(t, raP2))
		c.solicited()
		c.advance(time.Millisecond)
		c.ra(rtr1, raP1, adv(t, raP3)) // tail of the periodic burst
		for _, p := range []string{raP2, raP1, raP3} {
			c.advance(time.Millisecond)
			c.uni(rtr1, raP1, adv(t, p))
		}
		c.advance(2 * raConfirmDelay)
		if r, ok := c.abandoned(raP1); ok {
			t.Fatalf("P1 abandoned (%s) although the answer carried it", r)
		}
	})
}

func TestRATrackerSolicitAnsweredWithPrefix(t *testing.T) {
	c := newRAClock(t)
	c.ra(rtr1, raP1, adv(t, raP1))
	c.advance(5 * time.Second)
	c.ra(rtr1, raP1, adv(t, raP2))
	c.solicited()
	c.advance(10 * time.Millisecond)
	c.ra(rtr1, raP1, adv(t, raP2)) // rest of the periodic burst
	c.advance(300 * time.Millisecond)
	c.ra(rtr1, raP1, adv(t, raP1)) // the answer carries P1
	c.advance(10 * time.Millisecond)
	c.ra(rtr1, raP1, adv(t, raP2))
	for i := 0; i < 10; i++ {
		c.advance(time.Second)
		if r, ok := c.abandoned(raP1); ok {
			t.Fatalf("P1 abandoned (%s) although the solicited RA carried it", r)
		}
	}
}

func TestRATrackerSolicitUnanswered(t *testing.T) {
	// The RS goes unanswered (or cannot be sent): RAs before it are no
	// answer, so only the timeout is left.
	c := newRAClock(t)
	c.ra(rtr1, raP1, adv(t, raP1))
	c.advance(time.Second)
	if !c.ra(rtr1, raP1, adv(t, raP2)) {
		t.Fatal("no solicitation on the first RA without the active prefix")
	}
	// sending failed: no c.solicited()
	c.advance(2 * time.Second)
	c.ra(rtr1, raP1, adv(t, raP2))
	c.advance(10 * time.Second)
	if !c.ra(rtr1, raP1, adv(t, raP2)) {
		t.Fatal("no new solicitation after the first one could not be sent")
	}
	c.solicited()             // sent, never answered
	for i := 0; i < 17; i++ { // up to 30 s after the first miss
		c.advance(time.Second)
		if r, ok := c.abandoned(raP1); ok {
			t.Fatalf("P1 abandoned (%s) on an unanswered solicitation, step %d", r, i)
		}
	}
	c.advance(time.Second)
	reason, ok := c.abandoned(raP1)
	if !ok || reason != raReasonTimeout {
		t.Fatalf("abandoned = %q, %v; want %q 30 s after the first miss", reason, ok, raReasonTimeout)
	}
}

func TestRATrackerSplitBurst(t *testing.T) {
	// The router splits its PIOs across three RAs sent back to back, and
	// answers the RS the same way.
	burst := func(c *raClock) (solicit bool) {
		for _, p := range []string{raP1, raP2, raP3} {
			if c.ra(rtr1, raP1, adv(t, p)) {
				c.solicited()
				solicit = true
			}
			c.advance(5 * time.Millisecond)
		}
		return solicit
	}
	c := newRAClock(t)
	burst(c)
	for i := 0; i < 30; i++ {
		c.advance(10 * time.Second)
		if burst(c) {
			c.advance(300 * time.Millisecond)
			burst(c) // the answer
		}
		for j := 0; j < 9; j++ {
			c.advance(time.Second)
			if r, ok := c.abandoned(raP1); ok {
				t.Fatalf("round %d: P1 abandoned (%s) although every burst carries it", i, r)
			}
		}
	}
}

func TestRATrackerSplitRAsKeepPrefix(t *testing.T) {
	// One router splitting its PIOs across alternating RAs: P1 never misses
	// two in a row and is refreshed well within 30 s.
	c := newRAClock(t)
	for i := 0; i < 20; i++ {
		c.ra(rtr1, raP1, adv(t, raP1))
		c.advance(4 * time.Second)
		c.ra(rtr1, raP1, adv(t, raP2))
		c.advance(4 * time.Second)
		if r, ok := c.abandoned(raP1); ok {
			t.Fatalf("round %d: P1 abandoned (%s) although every other RA carries it", i, r)
		}
	}
}

func TestRATrackerRAWithoutGlobalsIsIgnored(t *testing.T) {
	// RAs that carry no global PIO (e.g. only RDNSS updates) are no evidence
	// that the prefix is gone.
	c := newRAClock(t)
	c.ra(rtr1, raP1, adv(t, raP1))
	c.ra(rtr2, raP1, adv(t, raP2))
	for i := 0; i < 5; i++ {
		c.advance(10 * time.Second)
		c.ra(rtr1, raP1)
	}
	if r, ok := c.abandoned(raP1); ok {
		t.Fatalf("P1 abandoned (%s) on RAs without any PIO", r)
	}
}

func TestRATrackerTwoRouters(t *testing.T) {
	t.Run("both advertise P1, one switches", func(t *testing.T) {
		c := newRAClock(t)
		c.ra(rtr1, raP1, adv(t, raP1))
		c.ra(rtr2, raP1, adv(t, raP1))
		for i := 0; i < 3; i++ {
			c.advance(3 * time.Second)
			c.ra(rtr1, raP1, adv(t, raP2))
			c.advance(3 * time.Second)
			c.ra(rtr2, raP1, adv(t, raP1))
		}
		if r, ok := c.abandoned(raP1); ok {
			t.Fatalf("P1 abandoned (%s) while router 2 still advertises it", r)
		}
	})

	t.Run("each keeps its own prefix", func(t *testing.T) {
		// Multihoming: router 2's frequent RAs never carry P1, router 1
		// refreshes it every 10 s.
		c := newRAClock(t)
		for i := 0; i < 10; i++ {
			c.ra(rtr1, raP1, adv(t, raP1))
			for j := 0; j < 3; j++ {
				c.advance(3 * time.Second)
				c.ra(rtr2, raP1, adv(t, raP2))
			}
			c.advance(time.Second)
			if r, ok := c.abandoned(raP1); ok {
				t.Fatalf("round %d: P1 abandoned (%s) although router 1 keeps advertising it", i, r)
			}
		}
	})

	t.Run("router holding P1 goes silent", func(t *testing.T) {
		// Only router 2 answers the RS, without P1. Router 1 says nothing
		// more, so there is no evidence that it dropped P1: keep it until
		// its lifetime ends, like the kernel.
		c := newRAClock(t)
		c.ra(rtr1, raP1, adv(t, raP1))
		c.advance(time.Second)
		if c.ra(rtr2, raP1, adv(t, raP2)) {
			c.solicited()
		}
		c.advance(300 * time.Millisecond)
		c.ra(rtr2, raP1, adv(t, raP2))
		for i := 0; i < 90; i++ { // 270 s of RAs from router 2 only
			c.advance(3 * time.Second)
			c.ra(rtr2, raP1, adv(t, raP2))
			if r, ok := c.abandoned(raP1); ok {
				t.Fatalf("P1 abandoned (%s) after %d s although its router went silent", r, 3*(i+1))
			}
		}
	})

	t.Run("router holding P1 drops it", func(t *testing.T) {
		// No RS gets out: the timeout applies once router 1 has been heard
		// without P1.
		c := newRAClock(t)
		c.ra(rtr1, raP1, adv(t, raP1))
		c.ra(rtr2, raP1, adv(t, raP2))
		for i := 0; i < 3; i++ {
			c.advance(3 * time.Second)
			c.ra(rtr2, raP1, adv(t, raP2))
		}
		c.advance(3 * time.Second)
		c.ra(rtr1, raP1, adv(t, raP3)) // 12 s after the first miss
		for i := 0; i < 5; i++ {
			c.advance(3 * time.Second)
			c.ra(rtr2, raP1, adv(t, raP2))
		}
		if r, ok := c.abandoned(raP1); ok {
			t.Fatalf("P1 abandoned (%s) 27 s after the first miss", r)
		}
		c.advance(3 * time.Second)
		reason, ok := c.abandoned(raP1)
		if !ok {
			t.Fatal("P1 not abandoned 30 s after RAs stopped carrying it")
		}
		if reason != raReasonTimeout {
			t.Fatalf("reason = %q, want %q", reason, raReasonTimeout)
		}
	})
}

func TestRATrackerTimeoutNeedsTwoRAs(t *testing.T) {
	// Never switch on a single RA, however long ago it was.
	c := newRAClock(t)
	c.ra(rtr1, raP1, adv(t, raP1))
	c.ra(rtr2, raP1, adv(t, raP2))
	c.advance(5 * time.Minute)
	if r, ok := c.abandoned(raP1); ok {
		t.Fatalf("P1 abandoned (%s) on a single RA without it", r)
	}
}

func TestRATrackerPrefixReappears(t *testing.T) {
	c := newRAClock(t)
	abandonP1(t, c)
	c.advance(3 * time.Second)
	c.ra(rtr1, raP2, adv(t, raP1), adv(t, raP2))
	if r, ok := c.abandoned(raP1); ok {
		t.Fatalf("P1 still abandoned (%s) after it reappeared", r)
	}
	// It takes two fresh misses and a fresh solicitation again to abandon it.
	c.advance(3 * time.Second)
	c.ra(rtr1, raP2, adv(t, raP2))
	if _, ok := c.abandoned(raP1); ok {
		t.Fatal("P1 abandoned on one RA after reappearing")
	}
	c.advance(3 * time.Second)
	c.ra(rtr1, raP2, adv(t, raP2))
	c.advance(10 * time.Second)
	if r, ok := c.abandoned(raP1); ok {
		t.Fatalf("P1 abandoned (%s) on an old solicitation after reappearing", r)
	}
}

func TestRATrackerNoFlapBack(t *testing.T) {
	// After the switch P1 -> P2 the selection stays on P2 while P1 is not
	// advertised again, whatever the RAs look like; the kernel keeps P1.
	c := newRAClock(t)
	abandonP1(t, c)
	p1, p2, p3 := mustCIDR(t, raP1), mustCIDR(t, raP2), mustCIDR(t, raP3)
	cands := []Candidate{
		{Prefix: p1, ValidUntil: c.now.Add(24 * time.Hour)},
		{Prefix: p2, ValidUntil: c.now.Add(time.Hour)},
		{Prefix: p3, ValidUntil: c.now.Add(time.Hour)},
	}
	cur := Select(dropAbandoned(cands, c.tr.abandoned(c.now)), p1)
	if !cidrEqual(cur, p2) {
		t.Fatalf("selected %v after P1 was abandoned, want %s", cur, raP2)
	}
	// Bursts split across three RAs, a second router with P3, RSs sent or
	// not, for 250 s (P1 was advertised with a 300 s lifetime).
	for i := 0; i < 25; i++ {
		for _, p := range []string{raP2, raP3, "2001:db8:dddd:1::/64"} {
			if c.ra(rtr1, cur.String(), adv(t, p)) && i%2 == 0 {
				c.solicited()
			}
			c.advance(5 * time.Millisecond)
		}
		c.advance(3 * time.Second)
		c.ra(rtr2, cur.String(), adv(t, raP3))
		c.advance(7 * time.Second)
		next := Select(dropAbandoned(cands, c.tr.abandoned(c.now)), cur)
		if !cidrEqual(next, p2) {
			t.Fatalf("round %d: selection moved %s -> %v while P1 is not advertised", i, cur, next)
		}
		if _, ok := c.abandoned(raP1); !ok {
			t.Fatalf("round %d: P1 no longer abandoned without being advertised", i)
		}
	}
	c.ra(rtr1, raP2, adv(t, raP1), adv(t, raP2))
	if r, ok := c.abandoned(raP1); ok {
		t.Fatalf("P1 still abandoned (%s) after it was advertised again", r)
	}
}

func TestRATrackerWithdrawal(t *testing.T) {
	c := newRAClock(t)
	c.ra(rtr1, raP1, adv(t, raP1))
	c.advance(3 * time.Second)
	c.ra(rtr1, raP1, withdraw(t, raP1), adv(t, raP2))
	reason, ok := c.abandoned(raP1)
	if !ok {
		t.Fatal("withdrawn P1 (valid lifetime 0) not abandoned")
	}
	if reason != raReasonWithdrawn {
		t.Fatalf("reason = %q, want %q", reason, raReasonWithdrawn)
	}

	t.Run("still advertised by another router", func(t *testing.T) {
		c := newRAClock(t)
		c.ra(rtr1, raP1, adv(t, raP1))
		c.ra(rtr2, raP1, adv(t, raP1))
		c.advance(3 * time.Second)
		c.ra(rtr1, raP1, withdraw(t, raP1), adv(t, raP2))
		if r, ok := c.abandoned(raP1); ok {
			t.Fatalf("P1 abandoned (%s) while router 2 still advertises it", r)
		}
	})
}

func TestRATrackerNoAlternativeKeepsPrefix(t *testing.T) {
	// Withdrawn or missing, but nothing else is advertised: there is nothing
	// better to move to.
	c := newRAClock(t)
	c.ra(rtr1, raP1, adv(t, raP1))
	c.advance(3 * time.Second)
	c.ra(rtr1, raP1, withdraw(t, raP1))
	if r, ok := c.abandoned(raP1); ok {
		t.Fatalf("P1 abandoned (%s) without an alternative prefix", r)
	}
}

func TestRATrackerForgetsExpiredPrefixes(t *testing.T) {
	c := newRAClock(t)
	c.ra(rtr1, raP1, raPIO{Prefix: mustCIDR(t, raP1), Valid: 60})
	c.ra(rtr1, raP1, raPIO{Prefix: mustCIDR(t, raP2), Valid: 60})
	c.solicited()
	c.advance(time.Second)
	c.ra(rtr1, raP1, raPIO{Prefix: mustCIDR(t, raP2), Valid: 60})
	c.advance(raConfirmDelay)
	if _, ok := c.abandoned(raP1); !ok {
		t.Fatal("precondition: P1 should be abandoned")
	}
	c.advance(3 * time.Hour)
	if got := c.tr.abandoned(c.now); len(got) != 0 {
		t.Fatalf("abandoned = %v after every lifetime expired, want none", got)
	}
	if len(c.tr.prefixes) != 0 || len(c.tr.routers) != 0 {
		t.Fatalf("tracker still holds %d prefixes / %d routers after expiry", len(c.tr.prefixes), len(c.tr.routers))
	}
}

func TestRATrackerSolicit(t *testing.T) {
	c := newRAClock(t)
	if c.ra(rtr1, raP1, adv(t, raP1)) {
		t.Fatal("solicited on an RA carrying the active prefix")
	}
	c.advance(3 * time.Second)
	if !c.ra(rtr1, raP1, adv(t, raP2)) {
		t.Fatal("no solicitation when a new prefix appeared without the active one")
	}
	c.advance(3 * time.Second)
	if c.ra(rtr1, raP1, adv(t, raP2), adv(t, raP3)) {
		t.Fatal("solicited again within 10 s")
	}
	c.advance(10 * time.Second)
	if c.ra(rtr1, raP1, adv(t, raP1), adv(t, raP2), adv(t, "2001:db8:dddd:1::/64")) {
		t.Fatal("solicited although the RA carries the active prefix")
	}
	c.advance(10 * time.Second)
	if c.ra(rtr1, "", adv(t, "2001:db8:eeee:1::/64")) {
		t.Fatal("solicited without an active prefix")
	}
}

func TestRATrackerReset(t *testing.T) {
	c := newRAClock(t)
	abandonP1(t, c)
	c.tr.reset()
	if got := c.tr.abandoned(c.now); len(got) != 0 {
		t.Fatalf("abandoned = %v after reset, want none", got)
	}
}

func TestSelectSkipsAbandoned(t *testing.T) {
	now := time.Now()
	p1, p2 := mustCIDR(t, raP1), mustCIDR(t, raP2)
	// The kernel still lists P1 (RA on-link route until its lifetime ends).
	cands := []Candidate{
		{Prefix: p1, ValidUntil: now.Add(24 * time.Hour)},
		{Prefix: p2, ValidUntil: now.Add(24 * time.Hour)},
	}
	if got := Select(dropAbandoned(cands, nil), p1); !cidrEqual(got, p1) {
		t.Fatalf("no RA seen: got %v, want sticky %s", got, p1)
	}
	got := Select(dropAbandoned(cands, map[string]string{raP1: raReasonMissing}), p1)
	if !cidrEqual(got, p2) {
		t.Fatalf("P1 abandoned: got %v, want %s", got, p2)
	}
	if got := Select(dropAbandoned(cands[:1], map[string]string{raP1: raReasonMissing}), p1); got != nil {
		t.Fatalf("only candidate abandoned: got %v, want nil (no change)", got)
	}
}

func TestSelectNeverSeenRAFallback(t *testing.T) {
	// Without any RA the tracker reports nothing, so selection is exactly
	// the netlink-only behaviour.
	tr := newRATracker()
	now := time.Now()
	p1 := mustCIDR(t, raP1)
	cands := []Candidate{
		{Prefix: p1, ValidUntil: now.Add(24 * time.Hour)},
		{Prefix: mustCIDR(t, raP2), ValidUntil: now.Add(24 * time.Hour)},
	}
	if got := Select(dropAbandoned(cands, tr.abandoned(now.Add(time.Hour))), p1); !cidrEqual(got, p1) {
		t.Fatalf("got %v, want sticky %s", got, p1)
	}
}

// levelCounter counts log records per level for one message.
type levelCounter struct {
	msg    string
	mu     sync.Mutex
	counts map[slog.Level]int
}

func (h *levelCounter) Enabled(context.Context, slog.Level) bool { return true }
func (h *levelCounter) Handle(_ context.Context, r slog.Record) error {
	if r.Message == h.msg {
		h.mu.Lock()
		h.counts[r.Level]++
		h.mu.Unlock()
	}
	return nil
}
func (h *levelCounter) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *levelCounter) WithGroup(string) slog.Handler      { return h }

func (h *levelCounter) get(l slog.Level) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.counts[l]
}

func TestWatchRAsWarnsOnceWhileSocketFails(t *testing.T) {
	h := &levelCounter{msg: raListenerFailed, counts: map[slog.Level]int{}}
	prevLog, prevOpen, prevRetry := slog.Default(), raOpen, raRetry
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prevLog); raOpen, raRetry = prevOpen, prevRetry })
	raRetry = time.Millisecond

	var opens atomic.Int32
	raOpen = func() (int, error) {
		opens.Add(1)
		return -1, unix.EPERM
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	NewRASource("raX").watchRAs(ctx)
	if opens.Load() < 3 {
		t.Fatalf("opens = %d, want retries", opens.Load())
	}
	if w := h.get(slog.LevelWarn); w != 1 {
		t.Fatalf("warnings = %d for %d failed opens, want 1", w, opens.Load())
	}
	if d := h.get(slog.LevelDebug); d < 2 {
		t.Fatalf("debug records = %d, want the later failures at debug", d)
	}

	// A socket that opened again re-arms the warning.
	h.counts = map[slog.Level]int{}
	opens.Store(0)
	raOpen = func() (int, error) {
		if opens.Add(1) != 3 {
			return -1, unix.EPERM
		}
		var p [2]int
		if err := unix.Pipe2(p[:], unix.O_CLOEXEC); err != nil {
			t.Error(err)
			return -1, err
		}
		unix.Close(p[1])
		return p[0], nil // not a socket: reading fails at once
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	NewRASource("raX").watchRAs(ctx2)
	if w := h.get(slog.LevelWarn); w != 2 {
		t.Fatalf("warnings = %d, want 2 (first failure, and the failure after a successful open)", w)
	}
}

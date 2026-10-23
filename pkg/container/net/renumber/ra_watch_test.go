//go:build linux

package renumber

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
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
	return c.tr.observe(net.ParseIP(router), pios, act, c.now)
}

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

func TestRATrackerAbandonsAfterTwoRAsWithoutPrefix(t *testing.T) {
	c := newRAClock(t)
	c.ra(rtr1, raP1, adv(t, raP1))
	c.advance(5 * time.Second)
	c.ra(rtr1, raP1, adv(t, raP2))
	if _, ok := c.abandoned(raP1); ok {
		t.Fatal("P1 abandoned on a single RA without it")
	}
	c.advance(5 * time.Second)
	c.ra(rtr1, raP1, adv(t, raP2))
	reason, ok := c.abandoned(raP1)
	if !ok {
		t.Fatal("P1 not abandoned after 2 consecutive RAs without it")
	}
	if reason != raReasonMissing {
		t.Fatalf("reason = %q, want %q", reason, raReasonMissing)
	}
	if _, ok := c.abandoned(raP2); ok {
		t.Fatal("P2 (still advertised) abandoned")
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
		c := newRAClock(t)
		c.ra(rtr1, raP1, adv(t, raP1))
		c.ra(rtr2, raP1, adv(t, raP2))
		for i := 0; i < 9; i++ { // 27 s of RAs from router 2 only
			c.advance(3 * time.Second)
			c.ra(rtr2, raP1, adv(t, raP2))
			if r, ok := c.abandoned(raP1); ok {
				t.Fatalf("P1 abandoned (%s) after %d s", r, 3*(i+1))
			}
		}
		c.advance(3 * time.Second) // 30 s since the first RA without P1
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
	c.ra(rtr1, raP1, adv(t, raP1))
	c.advance(3 * time.Second)
	c.ra(rtr1, raP1, adv(t, raP2))
	c.advance(3 * time.Second)
	c.ra(rtr1, raP1, adv(t, raP2))
	if _, ok := c.abandoned(raP1); !ok {
		t.Fatal("precondition: P1 should be abandoned")
	}
	c.advance(3 * time.Second)
	c.ra(rtr1, raP2, adv(t, raP1), adv(t, raP2))
	if r, ok := c.abandoned(raP1); ok {
		t.Fatalf("P1 still abandoned (%s) after it reappeared", r)
	}
	// It takes two fresh misses again to abandon it.
	c.advance(3 * time.Second)
	c.ra(rtr1, raP2, adv(t, raP2))
	if _, ok := c.abandoned(raP1); ok {
		t.Fatal("P1 abandoned on one RA after reappearing")
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
	c.ra(rtr1, raP1, raPIO{Prefix: mustCIDR(t, raP2), Valid: 60})
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
	c.ra(rtr1, raP1, adv(t, raP1))
	c.ra(rtr1, raP1, adv(t, raP2))
	c.ra(rtr1, raP1, adv(t, raP2))
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

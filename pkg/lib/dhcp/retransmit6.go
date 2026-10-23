package dhcp

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"net"
	"time"
)

// rtParams are the RFC 8415 §7.6 retransmission parameters of one message
// type. MRD is not modelled: callers bound the exchange with the context.
type rtParams struct {
	IRT time.Duration // initial retransmission time
	MRT time.Duration // maximum retransmission time (0 = no cap)
	MRC int           // maximum transmission count (0 = unlimited)
}

// Variables so tests can shrink the timers.
var (
	solicitRT = rtParams{IRT: 1 * time.Second, MRT: 3600 * time.Second}
	requestRT = rtParams{IRT: 1 * time.Second, MRT: 30 * time.Second, MRC: 10}
	renewRT   = rtParams{IRT: 10 * time.Second, MRT: 600 * time.Second}
	rebindRT  = rtParams{IRT: 10 * time.Second, MRT: 600 * time.Second}

	// rtRand returns the RAND term of RFC 8415 §15, in [-0.1, +0.1].
	rtRand = func() float64 { return (rand.Float64() - 0.5) / 5 }
)

// nextRT computes the next retransmission timeout (RFC 8415 §15): the first
// is IRT·(1+RAND), later ones 2·prev·(1+RAND), clamped to MRT·(1+RAND).
// prev is the previous timeout, 0 before the first transmission.
func nextRT(prev time.Duration, p rtParams, randTerm float64) time.Duration {
	if prev == 0 {
		return time.Duration(float64(p.IRT) * (1 + randTerm))
	}
	rt := time.Duration(2 * float64(prev) * (1 + randTerm))
	if p.MRT > 0 && rt > p.MRT {
		rt = time.Duration(float64(p.MRT) * (1 + randTerm))
	}
	return rt
}

// elapsedCS is the Elapsed Time option value (RFC 8415 §21.9): centiseconds
// since the first transmission, saturating at 0xffff.
func elapsedCS(d time.Duration) uint16 {
	cs := d / (10 * time.Millisecond)
	if cs < 0 {
		return 0
	}
	if cs > 0xffff {
		return 0xffff
	}
	return uint16(cs)
}

// setElapsed rewrites the Elapsed Time option of pkt, adding it if absent.
func setElapsed(pkt *Packet6, d time.Duration) {
	for i := range pkt.Options {
		if pkt.Options[i].Code == Opt6ElapsedTime {
			binary.BigEndian.PutUint16(pkt.Options[i].Data, elapsedCS(d))
			return
		}
	}
	pkt.Options = append(pkt.Options, Option6ElapsedTime(elapsedCS(d)))
}

// exchange sends pkt and waits for a want reply with the same transaction ID,
// retransmitting with exponential backoff (RFC 8415 §15) until one arrives,
// MRC transmissions have gone unanswered, or ctx ends. Every retransmission
// reuses the transaction ID and carries an updated Elapsed Time. A failed
// send (e.g. a tentative link-local during DAD) is retried by sendUntil
// without counting as a transmission.
func (c *Client6) exchange(ctx context.Context, conn net.PacketConn, pkt *Packet6, want byte, p rtParams) (*Packet6, error) {
	buf := make([]byte, 1500)
	var rt time.Duration
	var first time.Time
	for count := 1; ; count++ {
		if count == 1 {
			setElapsed(pkt, 0)
		} else {
			setElapsed(pkt, time.Since(first))
		}
		if err := sendUntil(ctx, conn, pkt, c.serverAddr, 250*time.Millisecond); err != nil {
			return nil, err
		}
		if count == 1 {
			first = time.Now()
		}
		rt = nextRT(rt, p, rtRand())
		deadline := time.Now().Add(rt)

		for time.Now().Before(deadline) {
			// Short read slices so a cancelled ctx is noticed promptly.
			rd := time.Now().Add(time.Second)
			if deadline.Before(rd) {
				rd = deadline
			}
			conn.SetReadDeadline(rd)
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				return nil, err
			}
			r, err := Unmarshal6(buf[:n])
			if err != nil || r.TransactionID != pkt.TransactionID {
				continue
			}
			if r.MsgType == want {
				return r, nil
			}
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if p.MRC > 0 && count >= p.MRC {
			return nil, fmt.Errorf("no %s after %d transmissions", MessageType6(want), count)
		}
	}
}

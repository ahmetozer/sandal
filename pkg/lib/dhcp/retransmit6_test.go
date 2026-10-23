package dhcp

import (
	"testing"
	"time"
)

func TestNextRTBackoffBounds(t *testing.T) {
	p := rtParams{IRT: time.Second, MRT: 30 * time.Second}
	for _, r := range []float64{-0.1, 0, 0.1} {
		rt := nextRT(0, p, r)
		lo, hi := time.Duration(0.9*float64(time.Second)), time.Duration(1.1*float64(time.Second))
		if rt < lo-1 || rt > hi+1 {
			t.Fatalf("first RT %v out of [%v,%v]", rt, lo, hi)
		}
	}
	// Zero jitter: 1,2,4,8,16, then capped at MRT=30.
	var rt time.Duration
	var got []time.Duration
	for range 7 {
		rt = nextRT(rt, p, 0)
		got = append(got, rt)
	}
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30}
	for i := range want {
		if got[i] != want[i]*time.Second {
			t.Fatalf("seq[%d] = %v, want %vs (all %v)", i, got[i], want[i], got)
		}
	}
	// With jitter the cap is MRT·(1+RAND), and doubling stays within ±10%.
	if rt := nextRT(20*time.Second, p, 0.1); rt != time.Duration(33*float64(time.Second)) {
		t.Fatalf("capped jittered RT = %v, want 33s", rt)
	}
	if rt := nextRT(4*time.Second, p, -0.1); rt != time.Duration(7.2*float64(time.Second)) {
		t.Fatalf("jittered RT = %v, want 7.2s", rt)
	}
}

func TestRtRandRange(t *testing.T) {
	for range 10000 {
		if r := rtRand(); r < -0.1 || r > 0.1 {
			t.Fatalf("rtRand = %v out of ±0.1", r)
		}
	}
}

func TestElapsedCS(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want uint16
	}{
		{0, 0}, {9 * time.Millisecond, 0}, {10 * time.Millisecond, 1},
		{1500 * time.Millisecond, 150}, {time.Hour, 0xffff}, {-time.Second, 0},
	} {
		if got := elapsedCS(tc.d); got != tc.want {
			t.Errorf("elapsedCS(%v) = %d, want %d", tc.d, got, tc.want)
		}
	}
}

func TestSetElapsedReplacesOrAdds(t *testing.T) {
	pkt := &Packet6{Options: Options6{Option6ElapsedTime(0)}}
	setElapsed(pkt, 2*time.Second)
	if len(pkt.Options) != 1 || pkt.Options[0].Data[0] != 0 || pkt.Options[0].Data[1] != 200 {
		t.Fatalf("options = %+v, want one elapsed=200", pkt.Options)
	}
	bare := &Packet6{}
	setElapsed(bare, time.Second)
	if len(bare.Options) != 1 || bare.Options[0].Code != Opt6ElapsedTime || bare.Options[0].Data[1] != 100 {
		t.Fatalf("options = %+v, want added elapsed=100", bare.Options)
	}
}

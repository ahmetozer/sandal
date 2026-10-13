//go:build linux

package net

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDecideIPv6Mode(t *testing.T) {
	cases := []struct {
		name string
		in   IPv6ModeSignals
		want string
	}{
		{
			name: "short routed delegation → pd",
			in:   IPv6ModeSignals{HasGlobal: true, HasShortDelegation: true},
			want: "pd",
		},
		{
			name: "short routed delegation, no global → still pd",
			in:   IPv6ModeSignals{HasGlobal: false, HasShortDelegation: true},
			want: "pd",
		},
		{
			name: "global address, no short delegation → ndp-proxy",
			in:   IPv6ModeSignals{HasGlobal: true, HasShortDelegation: false},
			want: "ndp-proxy",
		},
		{
			name: "nothing observable → fall back to ndp-proxy (safe default)",
			in:   IPv6ModeSignals{},
			want: "ndp-proxy",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DecideIPv6Mode(tc.in); got != tc.want {
				t.Errorf("DecideIPv6Mode(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestWaitForUpstreamRetriesUntilRouteAppears(t *testing.T) {
	orig := detectUpstreamFn
	t.Cleanup(func() { detectUpstreamFn = orig })
	n := 0
	detectUpstreamFn = func() (string, error) {
		n++
		if n < 3 {
			return "", errors.New("no default IPv6 or IPv4 route found")
		}
		return "eth0", nil
	}
	got, ok := WaitForUpstream(context.Background(), time.Millisecond)
	if !ok || got != "eth0" || n != 3 {
		t.Fatalf("got %q ok=%v after %d tries, want eth0 after 3", got, ok, n)
	}
}

func TestWaitForUpstreamStopsOnCancel(t *testing.T) {
	orig := detectUpstreamFn
	t.Cleanup(func() { detectUpstreamFn = orig })
	detectUpstreamFn = func() (string, error) { return "", errors.New("none") }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := WaitForUpstream(ctx, time.Hour); ok {
		t.Fatal("cancelled context must return ok=false")
	}
}

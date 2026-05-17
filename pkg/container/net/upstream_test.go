//go:build linux

package net

import "testing"

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

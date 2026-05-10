package dhcp

import (
	"bytes"
	"net"
	"testing"
)

func TestIAPrefixMarshalUnmarshal(t *testing.T) {
	prefix := net.ParseIP("2001:db8::").To16()
	p := IAPrefix{
		PreferredLifetime: 1800,
		ValidLifetime:     3600,
		PrefixLength:      60,
		Prefix:            prefix,
	}
	data := Option6IAPrefixOpt(p).Data
	if len(data) < 25 {
		t.Fatalf("IA_PREFIX option data too short: %d", len(data))
	}
	got, err := parseIAPrefix(data)
	if err != nil {
		t.Fatalf("parseIAPrefix: %v", err)
	}
	if got.PreferredLifetime != p.PreferredLifetime {
		t.Errorf("PreferredLifetime: got %d want %d", got.PreferredLifetime, p.PreferredLifetime)
	}
	if got.ValidLifetime != p.ValidLifetime {
		t.Errorf("ValidLifetime: got %d want %d", got.ValidLifetime, p.ValidLifetime)
	}
	if got.PrefixLength != p.PrefixLength {
		t.Errorf("PrefixLength: got %d want %d", got.PrefixLength, p.PrefixLength)
	}
	if !bytes.Equal(got.Prefix, p.Prefix) {
		t.Errorf("Prefix: got %x want %x", got.Prefix, p.Prefix)
	}
}

func TestIAPDRoundTrip(t *testing.T) {
	prefix := net.ParseIP("2001:db8::").To16()
	ia := &IAPD{
		IAID: 1,
		T1:   1800,
		T2:   2880,
		Options: Options6{
			Option6IAPrefixOpt(IAPrefix{
				PreferredLifetime: 3600,
				ValidLifetime:     7200,
				PrefixLength:      60,
				Prefix:            prefix,
			}),
		},
	}
	data := ia.Marshal()
	parsed := parseIAPD(data)
	if parsed == nil {
		t.Fatal("parseIAPD returned nil")
	}
	if parsed.IAID != 1 || parsed.T1 != 1800 || parsed.T2 != 2880 {
		t.Errorf("header mismatch: IAID=%d T1=%d T2=%d", parsed.IAID, parsed.T1, parsed.T2)
	}
	prefixes := parsed.Prefixes()
	if len(prefixes) != 1 {
		t.Fatalf("expected 1 prefix, got %d", len(prefixes))
	}
	if prefixes[0].PrefixLength != 60 {
		t.Errorf("PrefixLength: got %d want 60", prefixes[0].PrefixLength)
	}
	if !bytes.Equal(prefixes[0].Prefix, prefix) {
		t.Errorf("Prefix: got %x want %x", prefixes[0].Prefix, prefix)
	}
}

func TestOptionsIAPDAccessor(t *testing.T) {
	ia := &IAPD{IAID: 7, T1: 100, T2: 200}
	opts := Options6{Option6IAPDOpt(ia)}
	got := opts.IAPD()
	if got == nil {
		t.Fatal("IAPD() returned nil")
	}
	if got.IAID != 7 {
		t.Errorf("IAID: got %d want 7", got.IAID)
	}
}

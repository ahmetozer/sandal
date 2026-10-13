//go:build linux

package net

import (
	"encoding/json"
	"testing"
)

// TestLinkDynamicDefaultsTrueOnMissingKey: legacy configs persisted before
// the Dynamic field existed lack the key entirely. The zero-value of bool
// is false, which would silently opt those containers out of the renumber
// service. The contract: a missing "Dynamic" key must decode to
// Dynamic == true.
func TestLinkDynamicDefaultsTrueOnMissingKey(t *testing.T) {
	raw := []byte(`{"Id":"abc","Name":"eth0"}`)
	var l Link
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	if !l.Dynamic {
		t.Fatal("legacy JSON without Dynamic key should decode to Dynamic=true")
	}
}

// TestLinkDynamicExplicitFalseHonored covers the negation: when a user
// explicitly sets Dynamic=false in the config (e.g. via `-net "...;dynamic=false"`),
// that opt-out must survive a round-trip through JSON.
func TestLinkDynamicExplicitFalseHonored(t *testing.T) {
	raw := []byte(`{"Id":"abc","Name":"eth0","Dynamic":false}`)
	var l Link
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	if l.Dynamic {
		t.Fatal("explicit Dynamic=false must remain false after unmarshal")
	}
}

// TestLinkDynamicExplicitTrueHonored ensures Dynamic=true is preserved.
func TestLinkDynamicExplicitTrueHonored(t *testing.T) {
	raw := []byte(`{"Id":"abc","Dynamic":true}`)
	var l Link
	if err := json.Unmarshal(raw, &l); err != nil {
		t.Fatal(err)
	}
	if !l.Dynamic {
		t.Fatal("explicit Dynamic=true must remain true after unmarshal")
	}
}

func TestLinkDynamicAddrRoundTrip(t *testing.T) {
	in := Link{Id: "abc", Dynamic: true, DynamicAddr: []string{"%uv6%:192:0:2:23"}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Link
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.DynamicAddr) != 1 || out.DynamicAddr[0] != "%uv6%:192:0:2:23" {
		t.Fatalf("DynamicAddr lost in round trip: %v (json %s)", out.DynamicAddr, raw)
	}
}

func TestLinkDynamicAddrAbsentInLegacyJSON(t *testing.T) {
	var l Link
	if err := json.Unmarshal([]byte(`{"Id":"abc"}`), &l); err != nil {
		t.Fatal(err)
	}
	if l.DynamicAddr != nil {
		t.Fatalf("legacy JSON must decode to nil DynamicAddr, got %v", l.DynamicAddr)
	}
}

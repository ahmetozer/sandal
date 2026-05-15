//go:build linux

package net

import (
	"encoding/json"
	"testing"
)

// TestLinkDynamicDefaultsTrueOnMissingKey covers F3: configs persisted before
// commit d654ec5 (which added the Dynamic field) lack the key entirely. The
// zero-value of bool is false, which silently opts those containers out of the
// renumber service. The contract: a missing "Dynamic" key must decode to
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

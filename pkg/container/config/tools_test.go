package config

import "testing"

// NameFromConfigFile is the inverse of ConfigFileLoc. It must strip the
// ".json" suffix (not split on every "."), because ValidateName allows dots
// in names. The daemon previously split on "." and required exactly two
// parts, which silently dropped every dotted-name state file — breaking
// removal and resurrecting deleted containers (audit L6).
func TestNameFromConfigFile(t *testing.T) {
	cases := []struct {
		base string
		want string
		ok   bool
	}{
		{"x.json", "x", true},
		{"my.app.json", "my.app", true},
		{"a.b.c.json", "a.b.c", true},
		{"web-1_v2.json", "web-1_v2", true},
		{"noext", "", false},
		{"foo.txt", "", false},
		{".json", "", false},   // empty name
		{"..json", "", false},  // ValidateName rejects leading "."
		{"", "", false},
	}
	for _, tc := range cases {
		got, ok := NameFromConfigFile(tc.base)
		if got != tc.want || ok != tc.ok {
			t.Errorf("NameFromConfigFile(%q) = (%q,%v), want (%q,%v)", tc.base, got, ok, tc.want, tc.ok)
		}
	}
}

// Round-trip: the name encoded by ConfigFileLoc must be recoverable.
func TestNameFromConfigFileRoundTrip(t *testing.T) {
	for _, name := range []string{"x", "my.app", "a.b.c", "web-1_v2"} {
		base := name + ".json" // ConfigFileLoc joins BaseStateDir + name + ".json"
		got, ok := NameFromConfigFile(base)
		if !ok || got != name {
			t.Errorf("round-trip %q: got (%q,%v)", name, got, ok)
		}
	}
}

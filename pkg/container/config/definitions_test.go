package config

import (
	"bytes"
	"encoding/json"
	"testing"
)

// Generation must survive the disk encoding and Clone, and state files
// written before the field existed must keep loading as generation 0.
func TestGenerationEncoding(t *testing.T) {
	c := Config{Name: "web", Generation: 7}
	var back Config
	if err := json.Unmarshal(c.Json(), &back); err != nil {
		t.Fatal(err)
	}
	if back.Generation != 7 {
		t.Fatalf("round-trip Generation = %d, want 7", back.Generation)
	}
	if got := c.Clone().Generation; got != 7 {
		t.Fatalf("Clone Generation = %d, want 7", got)
	}

	var legacy Config
	if err := json.Unmarshal([]byte(`{"Name":"web","ContPid":100}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Generation != 0 || legacy.ContPid != 100 {
		t.Fatalf("legacy file decoded to Generation=%d ContPid=%d", legacy.Generation, legacy.ContPid)
	}
	if bytes.Contains((&Config{Name: "web"}).Json(), []byte("Generation")) {
		t.Fatal("generation 0 is written out; old files and new zero records should encode alike")
	}
}

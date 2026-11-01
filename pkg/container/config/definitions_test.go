package config

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ahmetozer/sandal/pkg/lib/stdio"
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

// The runtime-only IO and Ctx of an in-process run must stay out of the
// state file and out of the copies the controller keeps (Clone).
func TestRuntimeFieldsNotPersisted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out bytes.Buffer
	c := Config{Name: "web", IO: &stdio.Streams{Stdin: strings.NewReader("x"), Stdout: &out}, Ctx: ctx}

	data := c.Json()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"IO", "Ctx"} {
		if _, ok := fields[name]; ok {
			t.Errorf("state file holds %s: %s", name, data)
		}
	}

	clone := c.Clone()
	if clone.IO != nil || clone.Ctx != nil {
		t.Fatalf("Clone carried runtime fields: IO=%v Ctx=%v", clone.IO, clone.Ctx)
	}
	if clone.Name != "web" {
		t.Fatalf("Clone Name = %q, want web", clone.Name)
	}
	if c.IO == nil || c.Ctx != ctx {
		t.Fatal("Clone changed the original's runtime fields")
	}
}

func TestContextDefault(t *testing.T) {
	if (&Config{}).Context() != context.Background() {
		t.Fatal("Context() without Ctx is not context.Background()")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if (&Config{Ctx: ctx}).Context() != ctx {
		t.Fatal("Context() is not Ctx")
	}
}

//go:build linux || darwin

package clean

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/ahmetozer/sandal/pkg/env"
)

func withEnv(t *testing.T, lib, state, change string) {
	t.Helper()
	prev := struct{ l, s, c string }{env.LibDir, env.BaseStateDir, env.BaseChangeDir}
	env.LibDir = lib
	env.BaseStateDir = state
	env.BaseChangeDir = change
	t.Cleanup(func() {
		env.LibDir = prev.l
		env.BaseStateDir = prev.s
		env.BaseChangeDir = prev.c
	})
}

func TestPlanOrphans(t *testing.T) {
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	state := filepath.Join(lib, "state")
	change := filepath.Join(lib, "changedir")
	for _, d := range []string{state, change} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	withEnv(t, lib, state, change)

	// alive: has a state file AND a changedir entry — must be kept.
	if err := os.WriteFile(filepath.Join(state, "alive.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(change, "alive"), 0o755); err != nil {
		t.Fatal(err)
	}
	// ghost-dir: changedir without state file — orphan.
	if err := os.Mkdir(filepath.Join(change, "ghost-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	// ghost-img: changedir .img without state file — orphan.
	if err := os.WriteFile(filepath.Join(change, "ghost-img.img"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Usage set includes only "alive".
	usage := UsageSet{Containers: map[string]struct{}{"alive": {}}}

	actions := PlanOrphans(usage)
	var paths []string
	for _, a := range actions {
		paths = append(paths, filepath.Base(a.Path))
	}
	sort.Strings(paths)

	want := []string{"ghost-dir", "ghost-img.img"}
	if len(paths) != len(want) {
		t.Fatalf("got %v, want %v", paths, want)
	}
	for i := range paths {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

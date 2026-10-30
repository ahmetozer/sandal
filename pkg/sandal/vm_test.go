//go:build linux || darwin

package sandal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
)

// withVMStateEnv points the sandal dirs at a temp dir and returns the
// change dir.
func withVMStateEnv(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	lib := filepath.Join(tmp, "lib")
	state := filepath.Join(lib, "state")
	change := filepath.Join(lib, "changedir")
	run := filepath.Join(tmp, "run")
	for _, d := range []string{state, change, run} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	prev := struct{ l, r, s, c string }{env.LibDir, env.RunDir, env.BaseStateDir, env.BaseChangeDir}
	env.LibDir = lib
	env.RunDir = run
	env.BaseStateDir = state
	env.BaseChangeDir = change
	t.Cleanup(func() {
		env.LibDir = prev.l
		env.RunDir = prev.r
		env.BaseStateDir = prev.s
		env.BaseChangeDir = prev.c
	})
	return change
}

func exists(t *testing.T, p string) bool {
	t.Helper()
	_, err := os.Stat(p)
	return err == nil
}

func TestRemoveVMState(t *testing.T) {
	change := withVMStateEnv(t)
	c := &config.Config{Name: "t1", ChangeDir: filepath.Join(change, "t1")}
	if err := os.Mkdir(c.ChangeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{c.ChangeDir + ".img", c.ConfigFileLoc()} {
		if err := os.WriteFile(f, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	RemoveVMState(c)
	for _, p := range []string{c.ChangeDir, c.ChangeDir + ".img", c.ConfigFileLoc()} {
		if exists(t, p) {
			t.Errorf("%s still exists", p)
		}
	}

	// Paths are already gone: a second call must not fail.
	RemoveVMState(c)
}

func TestRemoveVMStateOutsideSandalArea(t *testing.T) {
	withVMStateEnv(t)
	outside := filepath.Join(t.TempDir(), "keep")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{Name: "t2", ChangeDir: outside}
	if err := os.WriteFile(c.ConfigFileLoc(), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	RemoveVMState(c)
	if !exists(t, outside) {
		t.Errorf("change dir outside the sandal area was removed")
	}
	if exists(t, c.ConfigFileLoc()) {
		t.Errorf("state file still exists")
	}
}

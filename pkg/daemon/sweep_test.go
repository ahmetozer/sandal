//go:build linux

package daemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/env"
)

func TestSweepStateTempFiles(t *testing.T) {
	dir := t.TempDir()
	prev := env.BaseStateDir
	env.BaseStateDir = dir
	t.Cleanup(func() { env.BaseStateDir = prev })

	old := time.Now().Add(-time.Hour)
	for name, mtime := range map[string]time.Time{
		".web.json.tmp-old":   old,
		".web.json.tmp-fresh": time.Now(),
		"web.json":            old,
		".hidden":             old,
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	sweepStateTempFiles(time.Minute)

	for name, want := range map[string]bool{
		".web.json.tmp-old":   false,
		".web.json.tmp-fresh": true,
		"web.json":            true,
		".hidden":             true,
	} {
		_, err := os.Stat(filepath.Join(dir, name))
		if (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", name, err == nil, want)
		}
	}
}

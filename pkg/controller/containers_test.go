//go:build linux

package controller

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
)

func TestContainersFromDirIgnoresNonStateFiles(t *testing.T) {
	dir := t.TempDir()
	prev := env.BaseStateDir
	env.BaseStateDir = dir
	t.Cleanup(func() { env.BaseStateDir = prev })

	web := (&config.Config{Name: "web"}).Json()
	app := (&config.Config{Name: "my.app"}).Json()
	for name, data := range map[string][]byte{
		"web.json":          web,
		".web.json.tmp-123": web, // temp file of an in-flight or killed write
		"notes.txt":         []byte("not a config"),
		"my.app.json":       app,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	confs, err := containersFromDir()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range confs {
		got = append(got, c.Name)
	}
	sort.Strings(got)
	if len(got) != 2 || got[0] != "my.app" || got[1] != "web" {
		t.Fatalf("listed %v, want [my.app web]", got)
	}
}

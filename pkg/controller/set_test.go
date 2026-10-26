//go:build linux

package controller

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
)

func TestSetContainerByDiskIsAtomic(t *testing.T) {
	dir := t.TempDir()
	prev := env.BaseStateDir
	env.BaseStateDir = dir
	t.Cleanup(func() { env.BaseStateDir = prev })

	a := &config.Config{Name: "atomic", ContArgs: []string{strings.Repeat("a", 4096)}}
	b := &config.Config{Name: "atomic", ContArgs: []string{strings.Repeat("b", 8192)}}
	if err := setContainerByDisk(a); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	errs := make(chan error, 1)
	go func() {
		for {
			select {
			case <-stop:
				close(errs)
				return
			default:
			}
			if _, err := LoadFile(a.ConfigFileLoc()); err != nil {
				errs <- err
				close(errs)
				return
			}
		}
	}()
	for i := 0; i < 2000; i++ {
		c := a
		if i%2 == 1 {
			c = b
		}
		if err := setContainerByDisk(c); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	if err := <-errs; err != nil {
		t.Fatalf("reader saw a partial state file: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(a.ConfigFileLoc()), ".*.tmp-*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

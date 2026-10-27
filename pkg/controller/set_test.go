//go:build linux

package controller

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	var stopOnce sync.Once
	stopReader := func() { stopOnce.Do(func() { close(stop) }) }
	defer stopReader() // a t.Fatal in the writer must not leave the reader spinning
	errs := make(chan error, 1)
	var reads atomic.Int64
	go func() {
		for {
			select {
			case <-stop:
				close(errs)
				return
			default:
			}
			reads.Add(1)
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
	stopReader()
	if err := <-errs; err != nil {
		t.Fatalf("reader saw a partial state file: %v", err)
	}
	if reads.Load() == 0 {
		t.Fatal("reader never ran, the test proved nothing")
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(a.ConfigFileLoc()), ".*.tmp-*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// useMemoryController puts the package in daemon (memory) mode over an empty
// temp state dir and restores the previous mode, list and dir afterwards.
func useMemoryController(t *testing.T) {
	t.Helper()
	prevType, prevContainers, prevDir := currentConrollerType, Containers, env.BaseStateDir
	containerListMu.Lock()
	prevList := containerList
	containerList = nil
	containerListMu.Unlock()

	currentConrollerType = ControllerTypeMemory
	Containers = containersFromMemory
	env.BaseStateDir = t.TempDir()

	t.Cleanup(func() {
		currentConrollerType, Containers, env.BaseStateDir = prevType, prevContainers, prevDir
		containerListMu.Lock()
		containerList = prevList
		containerListMu.Unlock()
	})
}

// The production lost update: the watcher read an older state file, recovery
// then published a new pid, and the watcher's late apply put the dead pid back
// in memory and on disk.
func TestStaleDiskEventCannotOverwriteNewerState(t *testing.T) {
	// daemon (memory controller) records the first instance
	useMemoryController(t)
	old := &config.Config{Name: "web", ContPid: 100, Status: "running"}
	if err := SetContainer(old); err != nil {
		t.Fatal(err)
	}
	staleOnDisk, err := LoadFile(old.ConfigFileLoc()) // the watcher read this version
	if err != nil {
		t.Fatal(err)
	}

	// recovery publishes the new pid before the watcher applies its read
	newer, _ := GetContainer("web")
	newer.ContPid = 200
	if err := SetContainer(newer); err != nil {
		t.Fatal(err)
	}

	// the watcher now applies its stale read
	if changed := LoadFromDisk(staleOnDisk); changed {
		t.Fatal("stale disk event replaced newer in-memory state")
	}
	got, _ := GetContainer("web")
	if got.ContPid != 200 {
		t.Fatalf("ContPid = %d, want 200 (lost update)", got.ContPid)
	}
	onDisk, _ := LoadFile(old.ConfigFileLoc())
	if onDisk.ContPid != 200 {
		t.Fatalf("disk ContPid = %d, want 200 (stale write-back)", onDisk.ContPid)
	}
}

// A file written by someone else (here a daemon-less CLI) carries a higher
// generation and must reach memory.
func TestLoadFromDiskAppliesNewerGeneration(t *testing.T) {
	useMemoryController(t)
	if err := SetContainer(&config.Config{Name: "web", ContPid: 100}); err != nil {
		t.Fatal(err)
	}
	external := &config.Config{Name: "web", ContPid: 300}
	if err := setContainerByDisk(external); err != nil {
		t.Fatal(err)
	}
	read, err := LoadFile(external.ConfigFileLoc())
	if err != nil {
		t.Fatal(err)
	}
	if !LoadFromDisk(read) {
		t.Fatal("newer generation from disk was ignored")
	}
	got, _ := GetContainer("web")
	if got.ContPid != 300 || got.Generation != 2 {
		t.Fatalf("memory ContPid=%d Generation=%d, want 300 and 2", got.ContPid, got.Generation)
	}
}

// State from before Generation: both sides are 0, so the old rule applies
// and differing content replaces memory; identical content is a no-op.
func TestLoadFromDiskLegacyGenerationZero(t *testing.T) {
	useMemoryController(t)
	containerListMu.Lock()
	containerList = []*config.Config{{Name: "web", ContPid: 100}} // loaded from a legacy file at startup
	containerListMu.Unlock()
	legacy := &config.Config{Name: "web", ContPid: 300}
	if err := os.WriteFile(legacy.ConfigFileLoc(), legacy.Json(), 0o644); err != nil {
		t.Fatal(err)
	}

	if !LoadFromDisk(legacy) {
		t.Fatal("legacy file with different content was ignored")
	}
	if got, _ := GetContainer("web"); got.ContPid != 300 {
		t.Fatalf("ContPid = %d, want 300", got.ContPid)
	}
	if LoadFromDisk(legacy.Clone()) {
		t.Fatal("identical legacy content reported a change")
	}
}

// LoadFromDisk only reads: applying a newer record or adding a new one must
// leave the file exactly as it was.
func TestLoadFromDiskNeverWrites(t *testing.T) {
	useMemoryController(t)
	if err := SetContainer(&config.Config{Name: "web", ContPid: 100}); err != nil {
		t.Fatal(err)
	}
	path := (&config.Config{Name: "web"}).ConfigFileLoc()
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)

	if !LoadFromDisk(&config.Config{Name: "web", ContPid: 300, Generation: 9}) {
		t.Fatal("newer generation was ignored")
	}
	after, _ := os.ReadFile(path)
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || !st.ModTime().Equal(past) {
		t.Fatalf("LoadFromDisk touched the file (mtime %v, want %v)", st.ModTime(), past)
	}

	// A new entry whose file exists is added, still without a write.
	other := &config.Config{Name: "db", ContPid: 400, Generation: 3}
	if err := os.WriteFile(other.ConfigFileLoc(), other.Json(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other.ConfigFileLoc(), past, past); err != nil {
		t.Fatal(err)
	}
	if !LoadFromDisk(other) {
		t.Fatal("new entry was not added")
	}
	if st, _ := os.Stat(other.ConfigFileLoc()); !st.ModTime().Equal(past) {
		t.Fatal("LoadFromDisk rewrote a new entry's file")
	}
	if left, _ := filepath.Glob(filepath.Join(env.BaseStateDir, ".*.tmp-*")); len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

// The watcher read a file, then the container was deleted (file and entry
// gone) before the read was applied. The late apply must not bring it back.
func TestLoadFromDiskDoesNotResurrectDeleted(t *testing.T) {
	useMemoryController(t)
	c := &config.Config{Name: "web", ContPid: 100}
	if err := SetContainer(c); err != nil {
		t.Fatal(err)
	}
	read, err := LoadFile(c.ConfigFileLoc())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(c.ConfigFileLoc()); err != nil {
		t.Fatal(err)
	}
	if err := DeleteContainer("web"); err != nil {
		t.Fatal(err)
	}

	if LoadFromDisk(read) {
		t.Fatal("stale read resurrected a deleted container")
	}
	if _, err := GetContainer("web"); err == nil {
		t.Fatal("deleted container is back in memory")
	}
	if _, err := os.Stat(c.ConfigFileLoc()); !os.IsNotExist(err) {
		t.Fatalf("state file recreated: %v", err)
	}
}

// Each set is a new generation above memory, the caller's copy and the
// file, and the caller's pointer is updated so its next set moves forward.
func TestSetContainerByMemoryBumpsGeneration(t *testing.T) {
	useMemoryController(t)
	c := &config.Config{Name: "web", ContPid: 100}
	for want := uint64(1); want <= 3; want++ {
		if err := SetContainer(c); err != nil {
			t.Fatal(err)
		}
		if c.Generation != want {
			t.Fatalf("caller Generation = %d, want %d", c.Generation, want)
		}
	}
	// A CLI POST decoded from an older read still lands above memory.
	if err := SetContainer(&config.Config{Name: "web", ContPid: 200, Generation: 1}); err != nil {
		t.Fatal(err)
	}
	got, _ := GetContainer("web")
	onDisk, _ := LoadFile(c.ConfigFileLoc())
	if got.Generation != 4 || onDisk.Generation != 4 || onDisk.ContPid != 200 {
		t.Fatalf("memory gen %d, disk gen %d pid %d; want 4, 4, 200", got.Generation, onDisk.Generation, onDisk.ContPid)
	}
}

// Without a daemon, a set bumps from the file's generation, so separate CLI
// processes (fresh Configs at generation 0) still move the file forward.
func TestSetContainerByDiskBumpsGeneration(t *testing.T) {
	dir := t.TempDir()
	prev := env.BaseStateDir
	env.BaseStateDir = dir
	t.Cleanup(func() { env.BaseStateDir = prev })

	for want := uint64(1); want <= 3; want++ {
		c := &config.Config{Name: "web", ContPid: int(want)}
		if err := setContainerByDisk(c); err != nil {
			t.Fatal(err)
		}
		onDisk, err := LoadFile(c.ConfigFileLoc())
		if err != nil {
			t.Fatal(err)
		}
		if onDisk.Generation != want || c.Generation != want {
			t.Fatalf("disk gen %d, caller gen %d, want %d", onDisk.Generation, c.Generation, want)
		}
	}
}

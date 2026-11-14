//go:build linux

package host

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/overlayfs"
	"github.com/ahmetozer/sandal/pkg/lib/loopdev"
)

// The daemon recovers containers that the CLI or an earlier daemon started.
// Their -chdir-type image change dir is not in this process's registry, so
// teardown unmounted it but left its loop device attached: one leaked loop
// per such recovery.
func TestUmountRootfsDetachesAnotherProcessesChangeDirLoop(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root for loop devices and mounts")
	}
	dir := t.TempDir()
	changeDir := filepath.Join(dir, "changedir", "rr-loop")
	// Mounted the way another process's run left it: not registered here.
	m, err := overlayfs.PrepareImageChangeDir(changeDir, "16m")
	if err != nil {
		t.Skipf("no loop-mounted ext4 here: %v", err)
	}
	t.Cleanup(func() { m.Cleanup() })

	c := &config.Config{
		Name:      "rr-loop",
		RootfsDir: filepath.Join(dir, "rootfs", "rr-loop"),
		ChangeDir: changeDir,
	}
	for _, err := range UmountRootfs(c) {
		t.Logf("UmountRootfs: %v", err)
	}

	// The kernel releases a detached loop once its last opener is gone,
	// a moment after the lazy unmount.
	got := loopdev.BackingFile(m.LoopDev.No)
	for deadline := time.Now().Add(3 * time.Second); got != "" && time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		got = loopdev.BackingFile(m.LoopDev.No)
	}
	if got != "" {
		t.Fatalf("loop%d still backs %s after the change dir was torn down", m.LoopDev.No, got)
	}
}

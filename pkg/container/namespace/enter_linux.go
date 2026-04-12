//go:build linux

package namespace

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Enter pins the current OS thread into the container at pid, switching
// all namespaces in the given Namespaces map. It performs the full sequence
// required for a Go thread to safely operate inside another task's mount
// namespace:
//
//  1. Open /proc/<pid>/root as a directory fd (before touching mnt ns) so
//     we can later re-anchor this thread's fs->root to the container root.
//  2. Unshare(cloneflags) to give this thread its own fs_struct; the
//     kernel refuses setns(CLONE_NEWNS) when fs->users > 1.
//  3. ns.SetNS() — respects per-entry user-defined targets (different pid
//     or bind-mounted ns file) when IsUserDefined is set.
//  4. If mnt is being entered (non-host), fchdir(rootFd) + chroot(".") +
//     chdir("/") so path lookups resolve against the container mount
//     namespace instead of the stale root dentry this thread still carries.
//
// The caller is responsible for runtime.LockOSThread BEFORE invoking Enter.
// Because namespace state is per-thread, callers that want the thread to
// remain in the target namespaces for the remainder of the goroutine must
// also refrain from UnlockOSThread — the Go runtime will destroy the
// thread when the goroutine returns instead of reusing it.
//
// If ns is empty, a default set is built from pid via DefaultsForPid; any
// user-defined entries with empty values are filled in via SetEmptyToPid.
// This mirrors the historical sequence used by pkg/container/exec/exec.go.
func Enter(pid int, ns Namespaces) error {
	if pid <= 0 {
		return fmt.Errorf("enter: invalid pid %d", pid)
	}
	if len(ns) == 0 {
		ns = DefaultsForPid(pid)
	} else {
		ns = ns.SetEmptyToPid(pid)
	}

	rootFd, err := os.Open(fmt.Sprintf("/proc/%d/root", pid))
	if err != nil {
		return fmt.Errorf("open container root /proc/%d/root: %w", pid, err)
	}
	defer rootFd.Close()

	if err := ns.Unshare(); err != nil {
		return fmt.Errorf("unshare: %w", err)
	}
	if err := ns.SetNS(); err != nil {
		return fmt.Errorf("setns: %w", err)
	}

	// Re-anchor fs->root iff we actually entered a (non-host) mnt ns.
	if mntConf, ok := ns["mnt"]; ok && !mntConf.IsHost {
		if err := unix.Fchdir(int(rootFd.Fd())); err != nil {
			return fmt.Errorf("fchdir container root: %w", err)
		}
		if err := unix.Chroot("."); err != nil {
			return fmt.Errorf("chroot container root: %w", err)
		}
		if err := unix.Chdir("/"); err != nil {
			return fmt.Errorf("chdir /: %w", err)
		}
	}
	return nil
}


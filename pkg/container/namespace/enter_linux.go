//go:build linux

package namespace

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Enter pins the current OS thread into the container at pid, switching
// all namespaces in the given Namespaces map. It performs the full sequence
// required for a Go thread to safely operate inside another task's mount
// namespace:
//
//  1. Open the root of the process that owns the mount namespace being
//     joined (/proc/<N>/root for a pid:<N> mnt target, see mntRoot) as a
//     directory fd, before touching the mnt ns, so we can later re-anchor
//     this thread's fs->root to it.
//  2. Unshare(cloneflags) to give this thread its own fs_struct; the
//     kernel refuses setns(CLONE_NEWNS) when fs->users > 1.
//  3. ns.SetNS() — respects per-entry user-defined targets (different pid
//     or bind-mounted ns file) when IsUserDefined is set.
//  4. If a mnt ns was joined, fchdir(rootFd) + chroot(".") + chdir("/") so
//     path lookups resolve against the joined mount namespace instead of
//     the stale root dentry this thread still carries.
//
// The caller is responsible for runtime.LockOSThread BEFORE invoking Enter.
// Because namespace state is per-thread, callers that want the thread to
// remain in the target namespaces for the remainder of the goroutine must
// also refrain from UnlockOSThread — the Go runtime will destroy the
// thread when the goroutine returns instead of reusing it.
//
// If ns is empty, a default set is built from pid via DefaultsForPid; any
// entries that are neither host nor user-defined are pointed at pid via
// SetEmptyToPid.
func Enter(pid int, ns Namespaces) error {
	if pid <= 0 {
		return fmt.Errorf("enter: invalid pid %d", pid)
	}
	if len(ns) == 0 {
		ns = DefaultsForPid(pid)
	} else {
		ns = ns.SetEmptyToPid(pid)
	}

	root, err := mntRoot(ns)
	if err != nil {
		return err
	}
	var rootFd *os.File
	if root != "" {
		rootFd, err = os.Open(root)
		if err != nil {
			return fmt.Errorf("open mount namespace root %s: %w", root, err)
		}
		defer rootFd.Close()
	}

	if err := ns.Unshare(); err != nil {
		return fmt.Errorf("unshare: %w", err)
	}
	// setns(CLONE_NEWNS) into the target mnt namespace requires the
	// calling thread's fs_struct to be private (fs->users == 1). When mnt
	// is being created (IsUserDefined=false, IsHost=false), Cloneflags
	// already included CLONE_NEWNS and ns.Unshare() handled this. When mnt
	// is user-defined, Cloneflags() skips it (we are not creating one), so
	// we must unshare CLONE_NEWNS here explicitly to get a private fs_struct
	// before SetNS attempts setns(CLONE_NEWNS).
	if mntConf, ok := ns["mnt"]; ok && mntConf.IsUserDefined {
		if err := unix.Unshare(syscall.CLONE_NEWNS); err != nil {
			return fmt.Errorf("unshare CLONE_NEWNS for user-defined mnt: %w", err)
		}
	}
	if err := ns.SetNS(); err != nil {
		return fmt.Errorf("setns: %w", err)
	}

	// Re-anchor fs->root iff we actually joined a mnt ns.
	if rootFd != nil {
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

// mntRoot returns the root directory to re-anchor to after joining the mnt
// entry of ns, or "" when no mount namespace is joined (no entry, host, or
// not user-defined). The root comes from the process that owns the joined
// mount namespace: /proc/<N>/root for a "pid:<N>" or "<N>" target. A
// file: target has no such process, so it is refused.
func mntRoot(ns Namespaces) (string, error) {
	conf, ok := ns["mnt"]
	if !ok || conf.IsHost || !conf.IsUserDefined {
		return "", nil
	}
	v := conf.String()
	pid, err := strconv.Atoi(strings.TrimPrefix(v, "pid:"))
	if err != nil || pid <= 0 {
		return "", fmt.Errorf("mnt target %q: a mount namespace can only be joined through a process (<pid> or pid:<pid>)", v)
	}
	return fmt.Sprintf("/proc/%d/root", pid), nil
}

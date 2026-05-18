//go:build linux

package guest

import (
	"fmt"
	"runtime"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/container/namespace"
	"golang.org/x/sys/unix"
)

// joinUserDefinedNamespaces switches the calling thread into any
// IsUserDefined namespaces from ns. It is intended to run inside the
// sandal-child process, after config load (which still needs the host
// mount namespace) and before any subsequent step that depends on the
// final namespace set (hostname, link configuration, mount setup, exec).
//
// Locks the calling goroutine to its OS thread and never unlocks: the
// rest of ContainerInitProc — including the terminating unix.Exec —
// must run on this same thread so the kernel-thread namespace state
// is what the user binary inherits.
//
// setns(CLONE_NEWNS) requires fs->users == 1. By the time this runs
// the Go runtime has already started additional m's (sysmon at least)
// with CLONE_FS, so we privatize the fs_struct via unshare first.
// `sandal run` rejects --ns-mnt <target> upstream, so this is defensive
// only — but Enter() makes the same compensation and we keep parity.
func joinUserDefinedNamespaces(ns namespace.Namespaces) error {
	if len(ns) == 0 {
		return nil
	}
	any := false
	for _, c := range ns {
		if c.IsUserDefined {
			any = true
			break
		}
	}
	if !any {
		return nil
	}

	runtime.LockOSThread()

	if mnt, ok := ns["mnt"]; ok && mnt.IsUserDefined {
		if err := unix.Unshare(syscall.CLONE_NEWNS); err != nil {
			return fmt.Errorf("unshare CLONE_NEWNS for user-defined mnt: %w", err)
		}
	}
	if err := ns.SetNS(); err != nil {
		return fmt.Errorf("join user-defined namespaces: %w", err)
	}
	return nil
}

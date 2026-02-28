//go:build linux

package cruntime

import (
	"fmt"
	"os"
	"strings"

	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/modprobe"
	"golang.org/x/sys/unix"
)

// IsVMInit returns true if sandal is running as PID 1 inside a VM
// (indicated by SANDAL_VM_ARGS being set).
func IsVMInit() bool {
	return os.Getpid() == 1 && os.Getenv("SANDAL_VM_ARGS") != ""
}

// isVM returns true if the container runtime is running inside a VM.
// Unlike IsVMInit (PID 1 check), this works for child processes too.
func isVM() bool {
	return os.Getenv("SANDAL_VM_ARGS") != ""
}

// VMInit performs early system setup when sandal runs as PID 1 (init) inside a VM.
// It mounts essential filesystems, switches from initramfs rootfs to a real tmpfs
// (so the container runtime can later pivot_root), loads virtiofs modules, and
// mounts virtiofs shares.
func VMInit() error {
	// Mount essential filesystems on the initramfs
	os.MkdirAll("/proc", 0755)
	if err := unix.Mount("proc", "/proc", "proc", 0, ""); err != nil {
		return fmt.Errorf("mount /proc: %w", err)
	}

	os.MkdirAll("/sys", 0755)
	if err := unix.Mount("sysfs", "/sys", "sysfs", 0, ""); err != nil {
		return fmt.Errorf("mount /sys: %w", err)
	}

	os.MkdirAll("/dev", 0755)
	unix.Mount("devtmpfs", "/dev", "devtmpfs", 0, "")

	// Load kernel modules before switch_root (modules live in the base initrd).
	for _, mod := range []string{"fuse", "virtiofs", "overlay", "loop", "squashfs"} {
		if err := modprobe.Load(mod); err != nil {
			fmt.Fprintf(os.Stderr, "modprobe %s: %v\n", mod, err)
		}
	}

	// The kernel's initramfs root (rootfs) doesn't support pivot_root.
	// Use switch_root approach: mount tmpfs, chroot into it.
	os.MkdirAll("/newroot", 0755)
	if err := unix.Mount("tmpfs", "/newroot", "tmpfs", 0, ""); err != nil {
		return fmt.Errorf("mount tmpfs on /newroot: %w", err)
	}

	// Copy init binary to the new root
	initData, err := os.ReadFile("/init")
	if err != nil {
		return fmt.Errorf("reading /init: %w", err)
	}
	if err := os.WriteFile("/newroot/init", initData, 0755); err != nil {
		return fmt.Errorf("writing /newroot/init: %w", err)
	}

	// Create directories in the new root
	for _, dir := range []string{"/proc", "/sys", "/dev", "/mnt", "/var/lib/sandal", "/var/run/sandal", "/tmp", "/etc"} {
		os.MkdirAll("/newroot"+dir, 0755)
	}

	// Mount proc/sys/dev in the new root
	unix.Mount("proc", "/newroot/proc", "proc", 0, "")
	unix.Mount("sysfs", "/newroot/sys", "sysfs", 0, "")
	unix.Mount("devtmpfs", "/newroot/dev", "devtmpfs", 0, "")

	// Chroot into the new tmpfs root (rootfs doesn't support pivot_root)
	if err := unix.Chroot("/newroot"); err != nil {
		return fmt.Errorf("chroot /newroot: %w", err)
	}
	os.Chdir("/")

	// Update BinLoc to use the binary in the new root
	env.BinLoc = "/init"

	// Mount virtiofs shares from SANDAL_VM_MOUNTS (format: tag=hostpath,tag=hostpath)
	// Each share is mounted at /mnt/<hostpath> to mirror the host filesystem layout.
	mountSpec := os.Getenv("SANDAL_VM_MOUNTS")
	if mountSpec == "" {
		return nil
	}

	for _, entry := range strings.Split(mountSpec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 {
			fmt.Fprintf(os.Stderr, "Warning: invalid mount spec %q\n", entry)
			continue
		}
		tag := parts[0]
		hostPath := parts[1]
		mountPoint := "/mnt" + hostPath
		os.MkdirAll(mountPoint, 0755)
		if err := unix.Mount(tag, mountPoint, "virtiofs", 0, ""); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to mount virtiofs %s at %s: %v\n", tag, mountPoint, err)
		}
	}

	return nil
}

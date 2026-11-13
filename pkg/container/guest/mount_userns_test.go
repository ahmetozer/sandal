//go:build linux

package guest

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/namespace"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"golang.org/x/sys/unix"
)

const (
	readOnlyRootEnv  = "SANDAL_TEST_RO_ROOT"
	readOnlyStageEnv = "SANDAL_TEST_RO_STAGE"
)

// TestReadOnlyRootHelper is not a test: TestReadOnlyRootInUserNS runs it
// twice. Stage "host" mounts the root filesystem from the host's user
// namespace, as sandal mounts the overlay rootfs before starting init, and
// starts stage "init" in a new user and mount namespace on top of it.
func TestReadOnlyRootHelper(t *testing.T) {
	root := os.Getenv(readOnlyRootEnv)
	switch os.Getenv(readOnlyStageEnv) {
	case "host":
		if err := unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, ""); err != nil {
			t.Fatalf("make / private: %v", err)
		}
		if err := unix.Mount("tmpfs", root, "tmpfs", 0, ""); err != nil {
			t.Fatalf("mount root tmpfs: %v", err)
		}
		idmap := []syscall.SysProcIDMap{{ContainerID: 0, HostID: 0, Size: crt.ProcSize}}
		cmd := exec.Command(os.Args[0], "-test.run=^TestReadOnlyRootHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), readOnlyStageEnv+"=init")
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS,
			UidMappings: idmap,
			GidMappings: idmap,
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("init stage: %v\n%s", err, out)
		}
	case "init":
		if err := unix.Chroot(root); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir("/"); err != nil {
			t.Fatal(err)
		}
		// purgeOldRoot detaches the old root first.
		if err := os.Mkdir("/.old_root", 0o700); err != nil {
			t.Fatal(err)
		}
		if err := unix.Mount("tmpfs", "/.old_root", "tmpfs", 0, ""); err != nil {
			t.Fatalf("mount /.old_root: %v", err)
		}

		empty := ""
		ns := namespace.Namespaces{"user": {UserValue: &empty}}
		if err := ns.Defaults(); err != nil {
			t.Fatal(err)
		}
		if err := purgeOldRoot(&config.Config{NS: ns, ReadOnly: true}); err != nil {
			t.Fatalf("purgeOldRoot: %v", err)
		}
		err := os.WriteFile("/written", []byte("x"), 0o644)
		if err == nil {
			t.Fatal("-ro: / is still writable")
		}
		if !errors.Is(err, syscall.EROFS) {
			t.Fatalf("write /: %v, want EROFS", err)
		}
	}
}

// TestReadOnlyRootInUserNS checks -ro still makes the container's root
// read-only when init runs in a user namespace (-ns-user "").
func TestReadOnlyRootInUserNS(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("e2e: requires root")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestReadOnlyRootHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), readOnlyRootEnv+"="+t.TempDir(), readOnlyStageEnv+"=host")
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNS}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

const sysfsStageEnv = "SANDAL_TEST_SYSFS_STAGE"

// TestSysfsHelper is not a test: TestSysfsInUserNS runs it twice. Stage
// "host" leaves only a read-only sysfs in its mount namespace, as on a host
// whose /sys is read-only (the sysfs is a new one, of a new network
// namespace, so the real /sys is not touched), and starts stage "init" in
// new user, mount and network namespaces, as container init runs.
func TestSysfsHelper(t *testing.T) {
	switch os.Getenv(sysfsStageEnv) {
	case "host":
		if err := unix.Mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, ""); err != nil {
			t.Fatalf("make / private: %v", err)
		}
		mounts, err := os.ReadFile("/proc/self/mountinfo")
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(mounts), "\n") {
			if f := strings.Fields(line); len(f) > 4 && strings.Contains(line, " - sysfs ") {
				_ = unix.Unmount(f[4], unix.MNT_DETACH)
			}
		}
		if err := unix.Mount("sysfs", "/sys", "sysfs", unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_NOSUID, "ro"); err != nil {
			t.Fatalf("mount read-only sysfs: %v", err)
		}
		idmap := []syscall.SysProcIDMap{{ContainerID: 0, HostID: 0, Size: crt.ProcSize}}
		cmd := exec.Command(os.Args[0], "-test.run=^TestSysfsHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), sysfsStageEnv+"=init")
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET,
			UidMappings: idmap,
			GidMappings: idmap,
		}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("init stage: %v\n%s", err, out)
		}
	case "init":
		empty := ""
		ns := namespace.Namespaces{"user": {UserValue: &empty}}
		if err := ns.Defaults(); err != nil {
			t.Fatal(err)
		}
		if err := mountSysfs(&config.Config{NS: ns}); err != nil {
			t.Fatalf("mountSysfs: %v", err)
		}
	}
}

// TestSysfsInUserNS checks container init can mount its /sys from a user
// namespace when the host's /sys is read-only.
func TestSysfsInUserNS(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("e2e: requires root")
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestSysfsHelper$", "-test.count=1")
	cmd.Env = append(os.Environ(), sysfsStageEnv+"=host")
	cmd.SysProcAttr = &syscall.SysProcAttr{Cloneflags: syscall.CLONE_NEWNS | syscall.CLONE_NEWNET}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

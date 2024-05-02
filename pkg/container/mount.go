package container

import (
	"log"
	"os"

	"github.com/ahmetozer/sandal/pkg/config"
	"golang.org/x/sys/unix"
)

const (
	sandalChildWorkdir = "/.sandal"
)

func childSysMounts(c *config.Config) {

	mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, "")
	mount("rootfs/", "rootfs/", "", unix.MS_BIND|unix.MS_REC, "")

	resolvFile := read("/etc/resolv.conf")
	hostsFile := read("/etc/hosts")

	os.Mkdir("rootfs/.old_root", 0700)
	if err := unix.PivotRoot("rootfs/", "rootfs/.old_root"); err != nil {
		log.Fatalf("unable to pivot root %s", err)
	}

	mount("tmpfs", sandalChildWorkdir, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "size=65536k,mode=755")
	mount("", sandalChildWorkdir, "", unix.MS_PRIVATE|unix.MS_REC, "")

	createResolv(c, resolvFile)
	createHosts(c, hostsFile)

	if err := os.Chdir("/"); err != nil {
		log.Fatalf("unable to chdir to / %s", err)
	}

	mount("proc", "/proc", "proc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_RELATIME, "")

	mount("cgroup", "/sys/fs/cgroup", "cgroup2", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_RELATIME, "nsdelegate,memory_recursiveprot")

	if c.Devtmpfs != "/dev" {
		mount("tmpfs", "/dev", "tmpfs", unix.MS_RELATIME, "size=65536k,mode=755")
	}
	if c.Devtmpfs != "" {
		mount("tmpfs", c.Devtmpfs, "devtmpfs", unix.MS_NOSUID, "size=65536k,mode=755")
	}

	mount("sysfs", "/sys", "sysfs", unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_NOSUID|unix.MS_RELATIME, "ro")

	mount("devpts", "/dev/pts", "devpts", unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_RELATIME, "gid=5,mode=620,ptmxmode=666")

	mount("shm", "/dev/shm", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_RELATIME, "size=64000k")

	// mount as private then unmount to remove access to old root

	mount("", sandalChildWorkdir, "", unix.MS_PRIVATE|unix.MS_REC, "")
	if err := unix.Unmount(sandalChildWorkdir, unix.MNT_DETACH); err != nil {
		log.Fatalf("unable to unmount %s %s", sandalChildWorkdir, err)
	}
	os.Remove(sandalChildWorkdir)

	mount("", "/.old_root", "", unix.MS_PRIVATE|unix.MS_REC, "")
	if err := unix.Unmount("/.old_root", unix.MNT_DETACH); err != nil {
		log.Fatalf("unable to unmount /.old_root %s", err)
	}

	if c.ReadOnly {
		mount("/", "/", "", unix.MS_REMOUNT|unix.MS_RDONLY, "")
	}

}

func mount(source, target, fstype string, flags uintptr, data string) {
	os.MkdirAll(target, 0600)
	if err := unix.Mount(source, target, fstype, flags, data); err != nil {
		log.Fatalf("unable to mount %s %s %s %s", source, target, fstype, err)
	}
}

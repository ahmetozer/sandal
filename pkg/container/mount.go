package container

import (
	"log"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/config"
	"golang.org/x/sys/unix"
)

const (
	sandalChildWorkdir = "/.sandal"
)

func childSysMounts(c *config.Config) {

	mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, "")
	mount("rootfs/", "rootfs/", "", unix.MS_BIND|unix.MS_REC, "")

	mountVolumes(c)

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

	if c.Devtmpfs != "/dev" {
		mount("tmpfs", "/dev", "tmpfs", unix.MS_RELATIME, "size=65536k,mode=755")
	}
	if c.Devtmpfs != "" {
		mount("tmpfs", c.Devtmpfs, "devtmpfs", unix.MS_NOSUID, "size=65536k,mode=755")
	}

	if _, err := os.Stat("/tmp"); os.IsNotExist(err) {
		mount("tmpfs", "/tmp", "tmpfs", unix.MS_NOSUID, "size=65536k,mode=777")
	}

	mount("sysfs", "/sys", "sysfs", unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_NOSUID|unix.MS_RELATIME, "ro")

	if c.NS["cgroup"].Value != "host" {
		mount("cgroup2", "/sys/fs/cgroup", "cgroup2", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_RELATIME, "nsdelegate,memory_recursiveprot")
	}

	mount("devpts", "/dev/pts", "devpts", unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_RELATIME, "gid=5,mode=620,ptmxmode=666")

	mount("shm", "/dev/shm", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_RELATIME, "size=64000k")

	// mount as private then unmount to remove access to old root

	mount("", sandalChildWorkdir, "", unix.MS_PRIVATE|unix.MS_REC, "")
	if err := unix.Unmount(sandalChildWorkdir, unix.MNT_DETACH); err != nil {
		log.Fatalf("unable to unmount %s %s", sandalChildWorkdir, err)
	}
	os.Remove(sandalChildWorkdir)

}

func purgeOldRoot(c *config.Config) {
	mount("", "/.old_root", "", unix.MS_PRIVATE|unix.MS_REC, "")
	if err := unix.Unmount("/.old_root", unix.MNT_DETACH); err != nil {
		log.Fatalf("unable to unmount /.old_root %s", err)
	}
	os.Remove("/.old_root")

	if c.ReadOnly {
		mount("/", "/", "", unix.MS_REMOUNT|unix.MS_RDONLY, "")
	}
}

func mount(source, target, fstype string, flags uintptr, data string) {

	// empty mount used for removing old root access from container
	if source != "" && source[0:1] == "/" {
		try := true
	CHECK:
		fileInfo, err := os.Stat(source)
		if os.IsNotExist(err) {
			if try {
				os.MkdirAll(filepath.Dir(source), 0600)
				try = false
				goto CHECK
			}
			log.Fatalf("The path %s does not exist.\n", source)
		}
		if err != nil {
			log.Fatalf("Error checking the path %s: %v\n", source, err)
		}

		if !fileInfo.IsDir() {
			os.MkdirAll(filepath.Dir(target), 0600)
			err = Touch(target)
			if err != nil {
				log.Fatalf("target %s touch error: %s", target, err.Error())
			}
		} else {
			os.MkdirAll(target, 0600)
		}
	} else {
		os.MkdirAll(target, 0600)
	}

	if err := unix.Mount(source, target, fstype, flags, data); err != nil {
		log.Fatalf("unable to mount %s %s %s %s", source, target, fstype, err)
	}
}

func mountVolumes(c *config.Config) {
	for _, v := range c.Volumes {

		m := strings.Split(v, ":")
		switch len(m) {
		//only path forwarded, destionation and mount iptions will generated
		case 1:
			m = append(m, m[0], "")
		// destionation path is provided but options are not provided
		case 2:
			m = append(m, "")
		case 3:
		default:
			log.Fatalf("unexpected mount configuration '%s'", v)
		}

		mount(m[0], path.Join("rootfs", m[1]), "", unix.MS_BIND, m[2])
	}
}

func Touch(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		createTry := false

	CREATE_FILE:
		file, err := os.Create(path)
		if os.IsNotExist(err) {
			os.MkdirAll(filepath.Dir(path), 0600)
			if !createTry {
				goto CREATE_FILE
			}
		} else if err != nil {
			return err
		}
		file.Close()
		return nil
	} else if err != nil {
		return err
	}
	return nil
}

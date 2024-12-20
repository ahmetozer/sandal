package cruntime

import (
	"log"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"golang.org/x/sys/unix"
)

const (
	sandalChildWorkdir = "/.sandal"
)

func childSysMounts(c *config.Config) {

	mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, "")
	mount(path.Join(c.RootfsDir), path.Join(c.RootfsDir), "", unix.MS_BIND|unix.MS_REC, "")

	mountVolumes(c)

	resolvFile := read("/etc/resolv.conf")
	hostsFile := read("/etc/hosts")

	os.Mkdir(path.Join(c.RootfsDir, ".old_root"), 0700)
	if err := unix.PivotRoot(path.Join(c.RootfsDir), path.Join(c.RootfsDir, ".old_root")); err != nil {
		slog.Error("childSysMounts", slog.String("action", "PivotRoot"), slog.Any("error", err))
		os.Exit(1)
	}

	mount("tmpfs", sandalChildWorkdir, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "size=65536k,mode=755")
	mount("", sandalChildWorkdir, "", unix.MS_PRIVATE|unix.MS_REC, "")

	createResolv(c, resolvFile)
	createHosts(c, hostsFile)

	if err := os.Chdir("/"); err != nil {
		slog.Error("childSysMounts", slog.String("action", "chdir"), slog.String("path", "/"), slog.Any("error", err))
		os.Exit(1)
	}

	mount("proc", "/proc", "proc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_RELATIME, "")

	if c.Devtmpfs != "/dev" {
		mount("tmpfs", "/dev", "tmpfs", unix.MS_RELATIME, "size=65536k,mode=755")
	}
	if c.Devtmpfs != "" {
		mount("tmpfs", c.Devtmpfs, "devtmpfs", unix.MS_NOSUID, "size=65536k,mode=755")
	}

	_, err := os.Stat("/tmp")
	if err == nil {
		slog.Debug("childSysMounts", slog.String("message", "/tmp exist"))
	} else {
		if os.IsNotExist(err) {
			mount("tmpfs", "/tmp", "tmpfs", unix.MS_NOSUID, "size=65536k,mode=1777")
			slog.Debug("childSysMounts", slog.String("mount", "tmp"))
		} else {
			slog.Info("childSysMounts", slog.String("action", "check"), slog.Any("error", err))
		}
	}
	err = os.Chmod("/tmp", 0o1777)
	slog.Debug("childSysMounts", slog.String("chmod", "1777"), slog.String("path", "/tmp"), slog.Any("error", err))

	mount("sysfs", "/sys", "sysfs", unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_NOSUID|unix.MS_RELATIME, "ro")

	if c.NS["cgroup"].Value != "host" {
		mount("cgroup2", "/sys/fs/cgroup", "cgroup2", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_RELATIME, "nsdelegate,memory_recursiveprot")
	}

	mount("devpts", "/dev/pts", "devpts", unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_RELATIME, "gid=5,mode=620,ptmxmode=666")

	mount("shm", "/dev/shm", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_RELATIME, "size=64000k")

	// mount as private then unmount to remove access to old root

	mount("", sandalChildWorkdir, "", unix.MS_PRIVATE|unix.MS_REC, "")
	if err := unix.Unmount(sandalChildWorkdir, unix.MNT_DETACH); err != nil {
		slog.Error("childSysMounts", slog.String("action", "unmount"), slog.String("path", sandalChildWorkdir), slog.Any("error", err))
		os.Exit(1)
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

	slog.Debug("mount", slog.String("source", source), slog.String("target", target), slog.String("fstype", fstype))

	// empty mount used for removing old root access from container
	if source != "" && source[0:1] == "/" {
		try := true
	CHECK:
		fileInfo, err := os.Stat(source)
		if os.IsNotExist(err) {
			if try {
				os.MkdirAll(filepath.Dir(source), 0o0600)
				try = false
				goto CHECK
			}
			slog.Error("mount", slog.String("action", "stat"), slog.String("source", source), slog.String("error", "path does not exist"))
		}
		if err != nil {
			slog.Error("mount", slog.String("action", "stat"), slog.String("source", source), slog.Any("error", err))
			os.Exit(1)
		}

		if !fileInfo.IsDir() {
			os.MkdirAll(filepath.Dir(target), 0o0600)
			err = Touch(target)
			if err != nil {
				slog.Error("mount", slog.String("target", target), slog.Any("error", err))
				os.Exit(1)
			}
		} else {
			os.MkdirAll(target, 0o0600)
		}
	} else {
		os.MkdirAll(target, 0o0600)
	}

	if err := unix.Mount(source, target, fstype, flags, data); err != nil {
		slog.Error("mount", slog.String("action", "unix.Mount"), slog.String("source", source), slog.String("target", target), slog.String("fstype", fstype), slog.Any("error", err))
		os.Exit(1)
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
		slog.Debug("mount", slog.Any("volume", m))

		mount(m[0], path.Join(c.RootfsDir, m[1]), "", unix.MS_BIND, m[2])
	}
}

func Touch(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		createTry := false

	CREATE_FILE:
		file, err := os.Create(path)
		if os.IsNotExist(err) {
			os.MkdirAll(filepath.Dir(path), 0o0600)
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

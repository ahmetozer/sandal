package cruntime

import (
	"fmt"
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

func childSysMounts(c *config.Config) error {

	err := mount("", "/", "", unix.MS_PRIVATE|unix.MS_REC, "")
	if err != nil {
		slog.Error("base private mount failed")
		return err
	}
	err = mount(path.Join(c.RootfsDir), path.Join(c.RootfsDir), "", unix.MS_BIND|unix.MS_REC, "")
	if err != nil {
		slog.Error("base private mount failed")
		return err
	}

	err = mountVolumes(c)
	if err != nil {
		slog.Error("mounting volumes failed")
		return err
	}

	resolvFile := read("/etc/resolv.conf")
	hostsFile := read("/etc/hosts")

	oldroot := path.Join(c.RootfsDir, ".old_root")
	newroot := path.Join(c.RootfsDir)
	os.Mkdir(oldroot, 0700)
	if err := unix.PivotRoot(newroot, oldroot); err != nil {
		slog.Error("pivoting root failed", "newroot", newroot, "oldroot", oldroot)
		return err
	}

	err = mount("tmpfs", sandalChildWorkdir, "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC, "size=65536k,mode=755")
	if err != nil {
		slog.Error("mounting tmpfs failed")
		return err
	}

	err = mount("", sandalChildWorkdir, "", unix.MS_PRIVATE|unix.MS_REC, "")
	if err != nil {
		slog.Error("privating filesystem failed")
		return err
	}

	err = createResolv(c, resolvFile)
	if err != nil {
		slog.Debug(string(*resolvFile))
		return err
	}
	err = createHosts(c, hostsFile)
	if err != nil {
		slog.Debug(string(*hostsFile))
		return err
	}

	if err := os.Chdir("/"); err != nil {
		return err
	}

	err = mount("proc", "/proc", "proc", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_RELATIME, "")
	if err != nil {
		return err
	}

	if c.Devtmpfs != "/dev" {
		err = mount("tmpfs", "/dev", "tmpfs", unix.MS_RELATIME, "size=65536k,mode=755")
		if err != nil {
			return err
		}

	}
	if c.Devtmpfs != "" {
		mount("tmpfs", c.Devtmpfs, "devtmpfs", unix.MS_NOSUID, "size=65536k,mode=755")
		if err != nil {
			return err
		}
	}

	_, err = os.Stat("/tmp")
	if err != nil {
		if os.IsNotExist(err) {
			err = mount("tmpfs", "/tmp", "tmpfs", unix.MS_NOSUID, "size=65536k,mode=1777")
			if err != nil {
				return err
			}
		}
	}
	err = os.Chmod("/tmp", 0o1777)
	if err != nil {
		return err
	}

	err = mount("sysfs", "/sys", "sysfs", unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_NOSUID|unix.MS_RELATIME, "ro")
	if err != nil {
		return err
	}

	if c.NS.GetNamespaceValue("cgroup") != "host" {
		err = mount("cgroup2", "/sys/fs/cgroup", "cgroup2", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_RELATIME, "nsdelegate,memory_recursiveprot")
		if err != nil {
			return err
		}

	}

	err = mount("devpts", "/dev/pts", "devpts", unix.MS_NOSUID|unix.MS_NOEXEC|unix.MS_RELATIME, "gid=5,mode=620,ptmxmode=666")
	if err != nil {
		return err
	}

	err = mount("shm", "/dev/shm", "tmpfs", unix.MS_NOSUID|unix.MS_NODEV|unix.MS_NOEXEC|unix.MS_RELATIME, "size=64000k")
	if err != nil {
		return err
	}

	// mount as private then unmount to remove access to old root

	err = mount("", sandalChildWorkdir, "", unix.MS_PRIVATE|unix.MS_REC, "")
	if err != nil {
		return err
	}

	if err := unix.Unmount(sandalChildWorkdir, unix.MNT_DETACH); err != nil {
		slog.Error("childSysMounts", slog.String("action", "unmount"), slog.String("path", sandalChildWorkdir), slog.Any("error", err))
		return err
	}
	os.Remove(sandalChildWorkdir)
	return nil

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

func mount(source, target, fstype string, flags uintptr, data string) error {

	slog.Debug("mount", slog.String("source", source), slog.String("target", target), slog.String("fstype", fstype))

	// empty mount used for removing old root access from container
	if source != "" && source[0:1] == "/" {
		retried := false
	CHECK_FOLDER:
		_, err := os.Stat(filepath.Dir(source))
		if os.IsNotExist(err) {
			if !retried {
				os.MkdirAll(filepath.Dir(source), 0o0600)
				retried = true
				goto CHECK_FOLDER
			}
			return fmt.Errorf("path %s does not exist and unable to created", filepath.Dir(source))
		}

		if err != nil {
			return err
		}

		fileInfo, err := os.Stat(source)
		if os.IsNotExist(err) {
			return fmt.Errorf("path does not exist %s", filepath.Dir(source))
		}
		if err != nil {
			return err
		}

		if !fileInfo.IsDir() {
			os.MkdirAll(filepath.Dir(target), 0o0600)
			err = Touch(target)
			if err != nil {
				return fmt.Errorf("unable to touch, %v", err)
			}
		} else {
			os.MkdirAll(target, 0o0600)
		}
	} else {
		os.MkdirAll(target, 0o0600)
	}

	if err := unix.Mount(source, target, fstype, flags, data); err != nil {
		return err
	}
	return nil
}

func mountVolumes(c *config.Config) error {
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
		default: // Including lenght zero 0
			return fmt.Errorf("unexpected mount configuration '%s'", v)
		}

		err := mount(m[0], path.Join(c.RootfsDir, m[1]), "", unix.MS_BIND, m[2])
		if err != nil {
			return err
		}
	}
	return nil
}

func Touch(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		createTry := false

	CREATE_FILE:
		file, err := os.Create(path)
		if os.IsNotExist(err) {
			os.MkdirAll(filepath.Dir(path), 0o0600)
			if !createTry {
				createTry = true
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

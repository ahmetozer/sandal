package cruntime

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"path"
	"strings"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/container/config"
)

func read(file string) *[]byte {
	resolv, err := os.ReadFile(file)
	if err != nil {
		log.Fatalf("unable to read %s: %s", file, err)
	}
	return &resolv

}

func createResolv(c *config.Config, d *[]byte) error {
	defer func() {
		d = nil
	}()

	if c.Resolv == "image" {
		return nil
	}

	if c.Resolv == "cp-n" {
		if file, err := os.Stat("/etc/resolv.conf"); err == nil {
			if file.Size() > 0 {
				return nil
			}
		}
	}

	if info, err := os.Lstat("/etc/resolv.conf"); err == nil {
		// Check if it's a symlink
		if info.Mode()&os.ModeSymlink != 0 {
			slog.Warn("symlink detected", "info", "/etc/resolv.conf is not a regular file, it will deleted. To keep orginal use --resolv=image")
		}
		os.Remove("/etc/resolv.conf")
	}

	if strings.Contains(".", c.Resolv) || strings.Contains(":", c.Resolv) {
		*d = nil
		nameServers := strings.Split(c.Resolv, ";")

		for _, ns := range nameServers {
			*d = append(*d, []byte("nameserver "+ns+"\n")...)
		}
	}

	err := os.MkdirAll(path.Join(sandalChildWorkdir, "/etc"), 0o0755)
	if err != nil {
		return fmt.Errorf("unable to create /etc : %s", err)
	}
	if err := os.WriteFile(path.Join(sandalChildWorkdir, "/etc/resolv.conf"), *d, 0644); err != nil {
		return fmt.Errorf("unable to write /etc/resolv.conf: %s", err)
	}
	err = mount(path.Join(sandalChildWorkdir, "/etc/resolv.conf"), "/etc/resolv.conf", "tmpfs", syscall.MS_BIND, "ro")
	if err != nil {
		slog.Error("unable to bind /etc/resolv.conf")
	}
	return err
}

func createHosts(c *config.Config, d *[]byte) error {
	defer func() {
		d = nil
	}()

	if c.Hosts == "image" {
		return nil
	}

	if c.Hosts == "cp-n" {
		if file, err := os.Stat("/etc/hosts"); err == nil {
			if file.Size() > 0 {
				return nil
			}
		}
	}

	err := os.MkdirAll(path.Join(sandalChildWorkdir, "/etc"), 0o0755)
	if err != nil {
		slog.Debug("unable to create /etc", "path", path.Join(sandalChildWorkdir, "/etc"))
		return err
	}
	if err := os.WriteFile(path.Join(sandalChildWorkdir, "/etc/hosts"), *d, 0o0644); err != nil {
		slog.Debug("unable to write /etc/hosts", "path", path.Join(sandalChildWorkdir, "/etc/hosts"))
		return err
	}

	f, err := os.OpenFile(path.Join(sandalChildWorkdir, "/etc/hosts"), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err = f.WriteString(fmt.Sprintf("127.0.0.1\t%s\n::1\t%s\n", c.Name, c.Name)); err != nil {
		return err
	}

	err = mount(path.Join(sandalChildWorkdir, "/etc/hosts"), "/etc/hosts", "tmpfs", syscall.MS_BIND, "ro")
	return err
}

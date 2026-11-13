//go:build linux

package guest

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/config"
	cmount "github.com/ahmetozer/sandal/pkg/container/mount"
	"golang.org/x/sys/unix"
)

func childSysNodes(c *config.Config) error {

	// because already host has these nodes and mirrored to container
	if c.Devtmpfs == "/dev" {
		return nil
	}
	// In a user namespace (-ns-user) the host's nodes are bound instead,
	// see bindHostNode.
	userNS := !c.NS.Get("user").IsHost
	var err error
	newOsNode := func(destination string, mode uint32, major, minor uint32) {
		if err != nil {
			return
		}
		if userNS {
			err = bindHostNode(destination)
			return
		}
		err = newOsNode(destination, mode, major, minor)
	}
	newOsNode("/dev/null", 0777, 1, 3)
	newOsNode("/dev/zero", 0666, 1, 5)
	newOsNode("/dev/full", 0666, 1, 7)
	newOsNode("/dev/random", 0666, 1, 8)
	newOsNode("/dev/urandom", 0666, 1, 9)
	newOsNode("/dev/tty", 0666, 5, 0)
	newOsNode("/dev/console", 0620, 5, 1)
	newOsNode("/dev/kmsg", 0620, 1, 11)
	newOsNode("/dev/tty0", 0620, 4, 0)
	if !userNS {
		newOsNode("/dev/ptmx", 0666, 5, 2)
	}
	newOsNode("/dev/net/tun", 0666, 10, 200)
	newOsNode("/dev/loop-control", 0660, 10, 237)
	newOsNode("/dev/fuse", 0666, 10, 229)

	// Standard symlinks expected by shells and programs.
	links := [][2]string{
		{"/dev/fd", "/proc/self/fd"},
		{"/dev/stdin", "/proc/self/fd/0"},
		{"/dev/stdout", "/proc/self/fd/1"},
		{"/dev/stderr", "/proc/self/fd/2"},
	}
	if userNS {
		// ptys come from the container's own devpts (ptmxmode=666),
		// not from the host's through a bound /dev/ptmx.
		links = append(links, [2]string{"/dev/ptmx", "pts/ptmx"})
	}
	for _, link := range links {
		if _, statErr := os.Lstat(link[0]); errors.Is(statErr, os.ErrNotExist) {
			if linkErr := os.Symlink(link[1], link[0]); linkErr != nil {
				slog.Warn("symlink creation failed", slog.String("link", link[0]), slog.Any("error", linkErr))
			}
		}
	}

	return err

}

func newOsNode(destination string, mode uint32, major, minor uint32) error {
	os.MkdirAll(filepath.Dir(destination), 0o0755) // no require to check error, it's ok if it exists
	_, error := os.Stat(destination)
	//return !os.IsNotExist(err)
	if !errors.Is(error, os.ErrNotExist) {
		slog.Warn("os node is exist", "destination", destination)
		return nil
	}

	if err := unix.Mknod(destination, unix.S_IFCHR|mode, int(unix.Mkdev(major, minor))); err != nil {
		slog.Debug("node creation error", slog.String("destination", destination), slog.Any("mode", mode), slog.Any("major", major), slog.Any("minor", minor))
		return fmt.Errorf("unable to create node %s: %s", destination, err)
	}
	os.Chmod(destination, os.FileMode(unix.S_IFCHR|mode))
	return nil
}

// bindHostNode bind-mounts the host's node at destination from the old root
// (still at /.old_root until purgeOldRoot). In a user namespace mknod needs
// CAP_MKNOD in the initial user namespace, and a node on the container's own
// tmpfs /dev could not be opened anyway: a tmpfs mounted there is nodev. A
// node the host does not have is skipped.
func bindHostNode(destination string) error {
	source := filepath.Join("/.old_root", destination)
	if _, err := os.Stat(source); err != nil {
		slog.Debug("host node not bound", slog.String("source", source), slog.Any("error", err))
		return nil
	}
	os.MkdirAll(filepath.Dir(destination), 0o0755) // no require to check error, it's ok if it exists
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		slog.Warn("os node is exist", "destination", destination)
		return nil
	}
	if err := cmount.Mount(source, destination, "", unix.MS_BIND, ""); err != nil {
		return fmt.Errorf("unable to bind node %s: %s", destination, err)
	}
	return nil
}

package cruntime

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func childSysNodes(Devtmpfs string) error {

	// because already host has these nodes and mirrored to container
	if Devtmpfs == "/dev" {
		return nil
	}
	var err error
	newOsNode := func(destination string, mode uint32, major, minor uint32) {
		if err != nil {
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
	newOsNode("/dev/ptmx", 0666, 5, 2)
	newOsNode("/dev/net/tun", 0666, 10, 200)

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

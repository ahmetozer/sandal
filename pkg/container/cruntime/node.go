package cruntime

import (
	"log"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"golang.org/x/sys/unix"
)

func childSysNodes(c *config.Config) {

	// because already host has these nodes and mirrored to container
	if c.Devtmpfs == "/dev" {
		return
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

}

func newOsNode(destination string, mode uint32, major, minor uint32) {
	os.MkdirAll(filepath.Dir(destination), 0o0755) // no require to check error, it's ok if it exists
	if err := unix.Mknod(destination, unix.S_IFCHR|mode, int(unix.Mkdev(major, minor))); err != nil {
		log.Fatalf("unable to create node %s: %s", destination, err)
	}
	os.Chmod(destination, os.FileMode(unix.S_IFCHR|mode))
}

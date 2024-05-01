package container

import (
	"log"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/config"
	"golang.org/x/sys/unix"
)

func childSysNodes(c *config.Config) {

	// because already host has these nodes and mirrored to container
	if c.Devtmpfs == "/dev" {
		return
	}

	newOsNode("/dev/null", unix.S_IFCHR|0666, 1, 3)
	newOsNode("/dev/zero", unix.S_IFCHR|0666, 1, 5)
	newOsNode("/dev/full", unix.S_IFCHR|0666, 1, 7)
	newOsNode("/dev/random", unix.S_IFCHR|0666, 1, 8)
	newOsNode("/dev/urandom", unix.S_IFCHR|0666, 1, 9)
	newOsNode("/dev/tty", unix.S_IFCHR|0666, 5, 0)
	newOsNode("/dev/console", unix.S_IFCHR|0666, 5, 1)
	newOsNode("/dev/tty0", unix.S_IFCHR|0666, 4, 0)
	newOsNode("/dev/ptmx", unix.S_IFCHR|0666, 5, 2)

}

func newOsNode(destination string, mode uint32, major, minor uint32) {
	os.MkdirAll(filepath.Dir(destination), 0755) // no require to check error, it's ok if it exists
	if err := unix.Mknod(destination, mode, int(unix.Mkdev(major, minor))); err != nil {
		log.Fatalf("unable to create node %s: %s", destination, err)
	}
}

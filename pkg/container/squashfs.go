package container

import (
	"fmt"
	"os"
	"path"
	"strconv"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/config"
)

func mountSquashfsFile(c *config.Config) (string, error) {

	err := os.MkdirAll(squashfsMountDir(c), 0755)
	if err != nil {
		return "", fmt.Errorf("creating rootfs directory: %s", err)
	}

	// Open the squashfs file
	squashfsFile, err := os.Open(c.SquashfsFile)
	if err != nil {
		return "", fmt.Errorf("opening squashfs file: %s", err)
	}
	defer squashfsFile.Close()

	c.LoopDevNo, err = FindFreeLoopDevice()
	if err != nil {
		return "", fmt.Errorf("cannot find free loop: %s", err)
	}
	err = AttachLoopDevice(c.LoopDevNo, squashfsFile)
	if err != nil {
		return "", fmt.Errorf("cannot attach loop: %s", err)
	}

	err = syscall.Mount(LOOP_DEVICE_PREFIX+strconv.Itoa(c.LoopDevNo), squashfsMountDir(c), "squashfs", syscall.MS_RDONLY, "")
	if err != nil {
		return "", fmt.Errorf("mount: %s", err)
	}

	return squashfsMountDir(c), nil
}

func umountSquashfsFile(c *config.Config) error {
	file := squashfsMountDir(c)
	err := syscall.Unmount(file, 0)
	if err != nil {
		return fmt.Errorf("umount: %s", err)
	}
	err = os.Remove(file)
	if err != nil {
		return fmt.Errorf("remove: %s", err)
	}
	return nil
}

func squashfsMountDir(c *config.Config) string {
	return path.Join(c.ContDir(), "lowerdir")
}

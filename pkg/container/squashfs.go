package container

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strconv"

	"github.com/ahmetozer/sandal/pkg/config"
	"golang.org/x/sys/unix"
)

func mountSquashfsFile(c *config.SquashFile) (SquashFSMountDir string, err error) {

	err = os.MkdirAll(getSquashfsMountDirName(c), 0o0755)
	if err != nil {
		return "", fmt.Errorf("creating rootfs directory: %s", err)
	}

	// Open the squashfs file
	squashfsFile, err := os.Open(c.File)
	if err != nil {
		return "", fmt.Errorf("opening squashfs file: %s", err)
	}
	defer squashfsFile.Close()

	c.LoopNo, err = FindFreeLoopDevice()
	if err != nil {
		return "", fmt.Errorf("cannot find free loop: %s", err)
	}
	err = AttachLoopDevice(c.LoopNo, squashfsFile)
	if err != nil {
		return "", fmt.Errorf("cannot attach loop: %s", err)
	}

	os.MkdirAll(getSquashfsMountDirName(c), 0o0755)
	err = unix.Mount(LOOP_DEVICE_PREFIX+strconv.Itoa(c.LoopNo), getSquashfsMountDirName(c), "squashfs", unix.MS_RDONLY, "")
	slog.Debug("mountSquashfsFile", slog.String("getSquashfsMountDirName", getSquashfsMountDirName(c)), slog.String("LOOP_DEVICE_PREFIX", LOOP_DEVICE_PREFIX), slog.Int("c.LoopNo", c.LoopNo))
	if err != nil {
		return "", fmt.Errorf("mount: %s", err)
	}

	return getSquashfsMountDirName(c), nil
}

func umountSquashfsFile(sq *config.SquashFile) error {
	mountDir := getSquashfsMountDirName(sq)
	var errs error
	if err := unix.Unmount(mountDir, 0); err != nil {
		if !os.IsNotExist(err) {
			errs = errors.Join(errs, fmt.Errorf("umount squashfs: %s", err))
		}
	}

	if err := os.Remove(mountDir); err != nil {
		if !os.IsNotExist(err) {
			errs = errors.Join(errs, fmt.Errorf("remove sqashfs dir: %s", err))
		}
	}

	if err := DetachLoopDevice(sq.LoopNo); err != nil {
		errs = errors.Join(errs, fmt.Errorf("deattach loop: %s", err))
	}
	if errs != nil {
		errs = errors.Join(fmt.Errorf("file: %s, loop: %d", sq.File, sq.LoopNo), errs)
	}
	return errs
}

func getSquashfsMountDirName(sq *config.SquashFile) string {
	return path.Join(config.BaseSquashFSMountDir, strconv.Itoa(int(sq.LoopNo)))
}

package diskimage

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

func Umount(sq *ImmutableImage) error {

	var errs error
	if err := unix.Unmount(sq.MountDir, 0); err != nil {
		if !os.IsNotExist(err) {
			errs = errors.Join(errs, fmt.Errorf("umount disk image: %s", err))
		}
	}

	if err := os.Remove(sq.MountDir); err != nil {
		if !os.IsNotExist(err) {
			errs = errors.Join(errs, fmt.Errorf("remove disk image dir: %s", err))
		}
	}

	if err := sq.LoopConfig.Detach(); err != nil {
		errs = errors.Join(errs, fmt.Errorf("deattach loop: %s", err))
	}

	if errs != nil {
		errs = errors.Join(fmt.Errorf("file: %s, loop: %d", sq.File, sq.LoopConfig.No), errs)
	}
	return errs
}

package container

import (
	"fmt"
	"os"

	"github.com/ahmetozer/sandal/pkg/config"
	"golang.org/x/sys/unix"
)

func MountRootfs(c *config.Config) error {
	// Mount overlay filesystem
	squasfsMount, err := mountSquashfsFile(c)
	if err != nil {
		return fmt.Errorf("mounting squashfs file: %s", err)
	}

	changeDir, err := createChangeDir(c)
	if err != nil {
		return fmt.Errorf("creating change directory: %s", err)
	}

	if err := os.MkdirAll(c.RootfsDir, 0755); err != nil {
		return fmt.Errorf("creating workdir: %s", err)
	}

	options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", squasfsMount, changeDir.uppper, changeDir.work)
	err = unix.Mount("overlay", c.RootfsDir, "overlay", 0, options)
	if err != nil {
		return fmt.Errorf("overlay: %s", err)
	}
	return nil

}

func UmountRootfs(c *config.Config) []error {
	errors := []error{}
	err := umountSquashfsFile(c)
	if err != nil {
		errors = append(errors, err)
	}

	err = unix.Unmount(c.RootfsDir, 0)
	if err != nil {
		if !os.IsNotExist(err) {
			errors = append(errors, err)
		}
	}
	err = os.Remove(c.RootfsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			errors = append(errors, err)
		}
	}

	if c.ChangeDir == "" && c.TmpSize != 0 {
		err = unix.Unmount(defaultChangeRoot(c), 0)
		if err != nil {
			if !os.IsNotExist(err) {
				errors = append(errors, err)
			}
		}
	}

	err = DetachLoopDevice(c.LoopDevNo)
	if err != nil {
		errors = append(errors, err)
	}
	if len(errors) == 0 {
		return nil
	}
	return errors
}

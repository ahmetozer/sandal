package container

import (
	"fmt"
	"os"
	"strings"

	"github.com/ahmetozer/sandal/pkg/config"
	"golang.org/x/sys/unix"
)

func MountRootfs(c *config.Config) error {
	// Mount overlay filesystem

	if c.SquashfsFile != "" {
		squasfsMount, err := mountSquashfsFile(c)
		if err != nil {
			return fmt.Errorf("mounting squashfs file: %s", err)
		}
		// this will last item of c.LowerDirs and lowest priority
		c.LowerDirs = append(c.LowerDirs, squasfsMount)
	}

	changeDir, err := createChangeDir(c)
	if err != nil {
		return fmt.Errorf("creating change directory: %s", err)
	}

	if err := os.MkdirAll(c.RootfsDir, 0755); err != nil {
		return fmt.Errorf("creating workdir: %s", err)
	}

	if len(c.LowerDirs) == 0 {
		if len(c.Volumes) == 0 {
			return fmt.Errorf("no lower dir is provided")
		}
	} else {
		// check folder is exist
		for _, folder := range c.LowerDirs {
			if _, err := os.Stat(folder); err != nil {
				return fmt.Errorf("folder %s is not exist: %e", folder, err)
			}
		}
	}

	if len(c.LowerDirs) != 0 {
		options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", strings.Join(c.LowerDirs, ":"), changeDir.uppper, changeDir.work)
		err = unix.Mount("overlay", c.RootfsDir, "overlay", 0, options)
		if err != nil {
			return fmt.Errorf("overlay: %s", err)
		}
	}
	return nil

}

func UmountRootfs(c *config.Config) []error {
	errors := []error{}
	var err error
	err = umountSquashfsFile(c)
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

	err = unix.Unmount(defaultChangeRoot(c), 0)
	if err != nil {
		if !os.IsNotExist(err) {
			errors = append(errors, err)
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

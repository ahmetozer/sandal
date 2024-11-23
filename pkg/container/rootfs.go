package container

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/ahmetozer/sandal/pkg/config"
	"golang.org/x/sys/unix"
)

func MountRootfs(c *config.Config) error {
	changeDir, err := prepareChangeDir(c)
	if err != nil {
		return fmt.Errorf("creating change directory: %s", err)
	}
	slog.Debug("MountRootfs", slog.String("upper", changeDir.upper), slog.String("work", changeDir.work))

	slog.Debug("MountRootfs", slog.String("rootfs", c.RootfsDir))
	if err := os.MkdirAll(c.RootfsDir, 0755); err != nil {
		return fmt.Errorf("creating workdir: %s", err)
	}

	var LowerDirs []string
	if len(c.Lower) == 0 {
		if len(c.Volumes) == 0 {
			return fmt.Errorf("no lower dir or volume is provided")
		}
	} else {
		// check folder is exist
		for _, path := range c.Lower {

			fileStat, err := os.Stat(path)
			slog.Debug("MountRootfs", slog.String("pathType", "lower"), slog.String("path", path))

			if err != nil {
				return fmt.Errorf("path %s is not exist: %s", path, err)
			}
			if fileStat.IsDir() {
				LowerDirs = append(LowerDirs, path)
			} else {
				sq := config.SquashFile{File: path}
				squasfsMountDir, err := mountSquashfsFile(&sq)
				slog.Debug("MountRootfs", slog.String("squasfsMountDir", squasfsMountDir))

				c.SquashFiles = append(c.SquashFiles, &sq)
				if err != nil {
					return fmt.Errorf("mounting squashfs file: %s", err)
				}
				// this will last item of c.LowerDirs and lowest priority
				LowerDirs = append(LowerDirs, squasfsMountDir)
			}

		}
	}

	if len(LowerDirs) != 0 {
		if s, err := isOverlayFS(changeDir.upper); err == nil {
			if s {
				return fmt.Errorf("upper (%s) is pointed to overlayfs. Kernel does not supports creating overlayfs under overlayfs. To overcome this, you can execute your container with temporary environment '-tmp', or you can point upper directory to real disk with '-udir' flag", changeDir.upper)
			}
		} else {
			return fmt.Errorf("unable to check overlayfs %s", err)
		}

		options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", strings.Join(LowerDirs, ":"), changeDir.upper, changeDir.work)
		err = unix.Mount("overlay", c.RootfsDir, "overlay", 0, options)
		if err != nil {
			slog.Info("MountRootfs", slog.String("aciton", "mount"), slog.String("type", "overlay"), slog.String("options", options), slog.String("name", c.Name))
			return fmt.Errorf("overlay: %s", err)
		}
	}
	return nil

}

func UmountRootfs(c *config.Config) []error {
	errs := []error{}
	var err error

	err = unix.Unmount(c.RootfsDir, 0)
	if err != nil {
		if !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	err = os.Remove(c.RootfsDir)
	if err != nil {
		if !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}

	if c.TmpSize != 0 {
		err = unix.Unmount(tmpdir(), 0)
		if err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, err)
			}
		}
	}

	for _, sq := range c.SquashFiles {
		err := umountSquashfsFile(sq)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}

func isOverlayFS(path string) (bool, error) {
	// Use unix.Statfs to get filesystem information about the path
	var stat unix.Statfs_t
	err := unix.Statfs(path, &stat)
	if err != nil {
		return false, fmt.Errorf("error getting statfs info for path %s: %w", path, err)
	}

	// Filesystem type is stored in stat.Type, check if it equals the value for overlayfs
	const overlayfs = 0x794c7630 // This is the magic number for overlayfs
	return stat.Type == overlayfs, nil
}

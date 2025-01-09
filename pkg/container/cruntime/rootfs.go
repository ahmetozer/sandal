package cruntime

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/cruntime/diskimage"
	"github.com/ahmetozer/sandal/pkg/container/cruntime/overlayfs"
	"golang.org/x/sys/unix"
)

func MountRootfs(c *config.Config) error {
	changeDir, err := overlayfs.PrepareChangeDir(c)
	if err != nil {
		return fmt.Errorf("creating change directory: %s", err)
	}
	slog.Debug("MountRootfs", slog.String("rootfs", c.RootfsDir), slog.String("upper", changeDir.GetUpper()), slog.String("work", changeDir.GetWork()))

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
		for _, argv := range c.Lower {
			path := strings.Split(argv, ":")[0]
			fileStat, err := os.Stat(path)
			slog.Debug("MountRootfs", slog.String("pathType", "lower"), slog.String("path", path))

			if err != nil {
				return fmt.Errorf("path %s is not exist: %s", path, err)
			}
			if fileStat.IsDir() {
				LowerDirs = append(LowerDirs, path)
			} else {
				// Detect file type
				//

				img, err := diskimage.Mount(argv)
				slog.Debug("MountRootfs", slog.Any("img", img))

				c.ImmutableImages = append(c.ImmutableImages, img)
				if err != nil {
					return fmt.Errorf("mounting file: %s", err)
				}
				// this will last item of c.LowerDirs and lowest priority
				LowerDirs = append(LowerDirs, img.MountDir)
			}

		}
	}

	if len(LowerDirs) > 0 {
		if s, err := changeDir.IsOverlayFS(); err == nil {
			if s {
				return fmt.Errorf("upper (%s) is pointed to overlayfs. Kernel does not supports creating overlayfs under overlayfs. To overcome this, you can execute your container with temporary environment '-tmp', or you can point upper directory to real disk with '-udir' flag", changeDir.GetUpper())
			}
		} else {
			return fmt.Errorf("unable to check overlayfs %s", err)
		}

		options := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", strings.Join(LowerDirs, ":"), changeDir.GetUpper(), changeDir.GetWork())
		err = unix.Mount("overlay", c.RootfsDir, "overlay", 0, options)
		slog.Debug("MountRootfs", slog.String("rootfs", c.RootfsDir), slog.Any("options", options))
		if err != nil {
			slog.Info("MountRootfs", slog.String("aciton", "mount"), slog.String("type", "overlay"), slog.String("options", options), slog.String("name", c.Name), slog.Any("error", err))
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
		err = unix.Unmount(overlayfs.Tmpdir(c), 0)
		if err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, err)
			}
		}
	}

	for _, sq := range c.ImmutableImages {
		err := diskimage.Umount(&sq)
		if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return errs
}

//go:build linux

package diskimage

import (
	"fmt"
	"log/slog"
	"os"
	"path"
	"strconv"
	"strings"

	cmount "github.com/ahmetozer/sandal/pkg/container/mount"
	"github.com/ahmetozer/sandal/pkg/env"
	detectfs "github.com/ahmetozer/sandal/pkg/lib/detectFs"
	"github.com/ahmetozer/sandal/pkg/lib/loopdev"
	"golang.org/x/sys/unix"
)

func Mount(path string) (ImmutableImage, error) {
	var (
		image ImmutableImage
		err   error
	)

	args := strings.Split(path, ":")
	switch len(args) {
	case 0:
		return image, fmt.Errorf("no file name provided")
	case 1:
		image.File = cmount.ResolvePath(args[0])
	default:
		image.File = cmount.ResolvePath(args[0])
		image.path = path
	}

	image.Type, image.info, err = image.detect()
	if err != nil {
		return image, err
	}

	// Resolve the desired geometry BEFORE allocating a loop, so we can look for
	// an existing mount to reuse. parseImagePath sets LoopConfig.Info.Offset for
	// partitioned images; squashfs is whole-file (offset 0).
	switch image.Type {
	case ImmutableImageTypeImgMBR, ImmutableImageTypeImgGPT:
		if err = image.parseImagePath(); err != nil {
			return image, err
		}
	case ImmutableImageTypeSquashfs:
		image.mountOptions = ""
	default:
		return image, fmt.Errorf("an unknown image type is chosen by the detect function")
	}
	var wantOffset uint64
	if image.LoopConfig.Info != nil {
		wantOffset = image.LoopConfig.Info.Offset
	}

	// Reuse: if this exact file+offset is already loop-mounted, point at the
	// existing mount instead of allocating a new loop and mounting again. This
	// makes mounting idempotent (no accumulation on restart/failed-recovery) and
	// shares identical read-only base images across containers.
	if mp, no, ok := findMountedImmutable(image.File, wantOffset, 0); ok {
		image.LoopConfig = loopdev.Config{No: no, Path: loopdev.DevicePath(no), Info: image.LoopConfig.Info}
		image.MountDir = mp
		slog.Debug("diskimage", slog.String("func", "mount"), slog.String("action", "reuse"),
			slog.String("file", image.File), slog.Int("loop", no), slog.String("mountDir", mp))
		return image, nil
	}

	// Miss: allocate a fresh loop. FindFreeLoopDevice returns a new Config, so
	// re-apply the partition offset (Info) computed above.
	savedInfo := image.LoopConfig.Info
	image.LoopConfig, err = loopdev.FindFreeLoopDevice()
	if err != nil {
		return image, fmt.Errorf("cannot find free loop: %s", err)
	}
	image.LoopConfig.Info = savedInfo

	err = image.unixMount()
	slog.Debug("diskimage", slog.String("func", "mount"), slog.Any("err", err))

	return image, err

}

func (c *ImmutableImage) unixMount() (err error) {

	c.MountDir = path.Join(env.BaseImmutableImageDir, strconv.Itoa(c.LoopConfig.No))

	err = c.LoopConfig.Attach(c.File)
	// imgFile.Close()
	if err != nil {
		return fmt.Errorf("cannot attach loop: %s", err)
	}

	err = os.MkdirAll(c.MountDir, 0o0755)
	if err != nil {
		c.LoopConfig.Detach() // don't leak the loop we just attached
		return fmt.Errorf("creating rootfs directory: %s", err)
	}

	fsType, err := detectfs.DetectFilesystem(c.LoopConfig.Path)
	if err != nil {
		c.LoopConfig.Detach()
		return err
	}
	err = cmount.Mount(c.LoopConfig.Path, c.MountDir, fsType, unix.MS_RDONLY, "")

	slog.Debug("diskMount", slog.Any("err", err), slog.String("mount-dir", c.MountDir),
		slog.String("loop-path", c.LoopConfig.Path), slog.String("autoFsType", fsType))

	if err != nil {
		// Detach the loop so a mount failure (e.g. a transient SD I/O error)
		// doesn't leave an orphaned attached loop device behind.
		c.LoopConfig.Detach()
		return fmt.Errorf("mount: %s", err)
	}

	return nil
}

package diskimage

import (
	"fmt"
	"log/slog"
	"os"
	"path"
	"strconv"
	"strings"

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
		return image, fmt.Errorf("no file name providen")
	case 1:
		image.File = args[0]
	default:
		image.File = args[0]
		image.path = path
	}

	image.Type, image.info, err = image.detect()
	if err != nil {
		return image, err
	}

	image.LoopConfig, err = loopdev.FindFreeLoopDevice()
	if err != nil {
		return image, fmt.Errorf("cannot find free loop: %s", err)
	}

	switch image.Type {
	case ImmutableImageTypeImgMBR, ImmutableImageTypeImgGPT:
		err = image.parseImagePath()
		if err != nil {
			return image, err
		}
	case ImmutableImageTypeSquashfs:
		image.mountOptions = ""
	default:
		return image, fmt.Errorf("an unknown image type is chosen by the detect function")
	}

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
		return fmt.Errorf("creating rootfs directory: %s", err)
	}

	fsType, err := detectfs.DetectFilesystem(c.LoopConfig.Path)
	if err != nil {
		return err
	}
	err = unix.Mount(c.LoopConfig.Path, c.MountDir, fsType, unix.MS_RDONLY, "")

	slog.Debug("diskMount", slog.Any("err", err), slog.String("mount-dir", c.MountDir),
		slog.String("loop-path", c.LoopConfig.Path), slog.String("autoFsType", fsType))

	if err != nil {
		return fmt.Errorf("mount: %s", err)
	}

	return nil
}

type ImmutableImages []ImmutableImage

func (images ImmutableImages) Contains(image ImmutableImage) bool {
	for i := range images {
		if images[i].File == image.File {
			return true
		}
	}
	return false
}
func (images *ImmutableImages) ReplaceWith(image ImmutableImage) bool {
	for i := range *images {
		if (*images)[i].File == image.File {
			(*images)[i] = image
			return true
		}
	}
	return false
}

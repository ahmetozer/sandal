//go:build linux

package overlayfs

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/env"
	"golang.org/x/sys/unix"
)

type ChangesDir struct {
	upper string
	work  string
}

func (c ChangesDir) GetUpper() string {
	return c.upper
}

func (c ChangesDir) GetWork() string {
	return c.work
}

func Tmpdir(c *config.Config) string {
	return path.Join(env.RunDir, "tmpfs", "changes", c.Name)
}

// GetChangeDir returns the change directory paths for a container without
// creating directories or mounting filesystems.
func GetChangeDir(c *config.Config) ChangesDir {
	dir := ChangesDir{
		work:  path.Join(c.ChangeDir, "work"),
		upper: path.Join(c.ChangeDir, "upper"),
	}
	if c.TmpSize != 0 {
		tmpdir := Tmpdir(c)
		dir.work = path.Join(tmpdir, "work")
		dir.upper = path.Join(tmpdir, "upper")
	}
	return dir
}

func PrepareChangeDir(c *config.Config) (ChangesDir, error) {
	var errs error
	dir := ChangesDir{
		work:  path.Join(c.ChangeDir, "work"),
		upper: path.Join(c.ChangeDir, "upper"),
	}
	// if temp size is set, create a tmpfs and allocate changes under tmpfs
	isVm, _ := env.IsVM()
	if c.TmpSize != 0 {
		tmpdir := Tmpdir(c)

		dir.work = path.Join(tmpdir, "work")
		dir.upper = path.Join(tmpdir, "upper")

		sizeBytes := uint64(c.TmpSize * 1024 * 1024) // 1MB
		if err := os.MkdirAll(tmpdir, 0o0755); err != nil {
			return dir, fmt.Errorf("creating %s directory: %s", tmpdir, err)
		}
		// Mount the tmpfs
		err := unix.Mount("tmpfs", tmpdir, "tmpfs", uintptr(0), fmt.Sprintf("size=%d", sizeBytes))
		if err != nil {
			return dir, fmt.Errorf("tmpfs: %s", err)
		}

	} else if isVm {
		// VM mode: VirtioFS doesn't support overlayfs kernel operations.
		// Use a loop-mounted ext4 disk image for the change dir instead.
		mount, err := prepareVMChangeDir(c.ChangeDir)
		if err != nil {
			return dir, fmt.Errorf("vm change dir: %w", err)
		}
		RegisterVMChangeMount(c.ChangeDir, mount)
		slog.Debug("PrepareChangeDir", slog.String("vmImage", mount.ImagePath),
			slog.String("loopDev", mount.LoopDev.Path))
	}
	slog.Debug("PrepareChangeDir", slog.Any("dir", dir))

	if err := os.MkdirAll(dir.work, 0o0755); err != nil {
		errs = errors.Join(err, fmt.Errorf("creating %s directory: %s", dir.work, err))
	}

	if err := os.MkdirAll(dir.upper, 0o0755); err != nil {
		errs = errors.Join(err, fmt.Errorf("creating %s directory: %s", dir.upper, err))
	}

	return dir, errs
}

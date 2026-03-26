//go:build linux

package overlayfs

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"

	"github.com/ahmetozer/sandal/pkg/container/config"
	cmount "github.com/ahmetozer/sandal/pkg/container/mount"
	"github.com/ahmetozer/sandal/pkg/env"
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

// resolveChangeDirType determines whether to use "folder" or "image" mode.
// "auto" picks "folder" when overlayfs is supported on the change dir's
// parent filesystem, otherwise falls back to "image".
func resolveChangeDirType(c *config.Config) string {
	typ := c.ChangeDirType
	if typ == "" {
		typ = "auto"
	}
	if typ != "auto" {
		return typ
	}

	// auto: probe the parent directory
	parent := path.Dir(c.ChangeDir)
	if err := os.MkdirAll(parent, 0755); err != nil {
		slog.Debug("resolveChangeDirType", slog.String("fallback", "image"), slog.Any("error", err))
		return "image"
	}

	// Check if parent is already on overlayfs (nested overlayfs unsupported)
	if isOvl, _ := IsOverlayFS(parent); isOvl {
		return "image"
	}

	// Try creating work dir — overlayfs requires rename_whiteout support.
	// A quick test: create and remove a test directory.
	testDir := path.Join(parent, ".sandal-probe")
	if err := os.MkdirAll(testDir, 0755); err != nil {
		return "image"
	}
	os.Remove(testDir)

	// If we're in a VM, VirtioFS doesn't support overlayfs upper
	if isVm, _ := env.IsVM(); isVm {
		return "image"
	}

	return "folder"
}

func PrepareChangeDir(c *config.Config) (ChangesDir, error) {
	var errs error
	dir := ChangesDir{
		work:  path.Join(c.ChangeDir, "work"),
		upper: path.Join(c.ChangeDir, "upper"),
	}

	// tmpfs overrides everything
	if c.TmpSize != 0 {
		tmpdir := Tmpdir(c)

		dir.work = path.Join(tmpdir, "work")
		dir.upper = path.Join(tmpdir, "upper")

		sizeBytes := uint64(c.TmpSize * 1024 * 1024)
		if err := os.MkdirAll(tmpdir, 0o0755); err != nil {
			return dir, fmt.Errorf("creating %s directory: %s", tmpdir, err)
		}
		err := cmount.Mount("tmpfs", tmpdir, "tmpfs", uintptr(0), fmt.Sprintf("size=%d", sizeBytes))
		if err != nil {
			return dir, fmt.Errorf("tmpfs: %s", err)
		}
	} else {
		cdType := resolveChangeDirType(c)
		slog.Debug("PrepareChangeDir", slog.String("changeDirType", cdType))

		if cdType == "image" {
			mount, err := prepareImageChangeDir(c.ChangeDir, c.ChangeDirSize)
			if err != nil {
				return dir, fmt.Errorf("change dir image: %w", err)
			}
			RegisterImageChangeMount(c.ChangeDir, mount)
			slog.Debug("PrepareChangeDir", slog.String("image", mount.ImagePath),
				slog.String("loopDev", mount.LoopDev.Path))
		} else if c.ChangeDirSize != "" {
			slog.Warn("PrepareChangeDir", slog.String("msg", "-csize is ignored when change dir type is folder"))
		}
		// "folder" mode: use change dir directly, no extra setup needed
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

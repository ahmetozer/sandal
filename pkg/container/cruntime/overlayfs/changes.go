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

func PrepareChangeDir(c *config.Config) (ChangesDir, error) {
	var errs error
	dir := ChangesDir{
		work:  path.Join(c.ChangeDir, "work"),
		upper: path.Join(c.ChangeDir, "upper"),
	}
	// if temp size is set, create a tmpfs and allocate changes under tmpfs
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

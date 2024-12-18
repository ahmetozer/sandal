package overlayfs

import (
	"errors"
	"fmt"
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

func Tmpdir() string {
	return path.Join(env.RunDir, "tmpfs", "changes")
}

func PrepareChangeDir(c *config.Config) (ChangesDir, error) {
	var errs error
	dir := ChangesDir{
		work:  c.Workdir,
		upper: c.Upperdir,
	}
	// if temp size is set, create a tmpfs and allocate changes under tmpfs
	if c.TmpSize != 0 {
		tmpdir := Tmpdir()

		dir.work = path.Join(tmpdir, "work")
		dir.upper = path.Join(tmpdir, "upper")

		if dir.upper == c.Upperdir {
			errs = errors.Join(errs, fmt.Errorf("tmpfs (%s) cannot be the same as upperdir (%s)", dir.upper, c.Upperdir))
		}
		if dir.work == c.Workdir {
			errs = errors.Join(errs, fmt.Errorf("tmpfs (%s) cannot be the same as workdir (%s)", dir.upper, c.Workdir))
		}

		if errs != nil {
			return dir, errs
		}

		sizeBytes := uint64(c.TmpSize * 1024 * 1024) // 100MB
		if err := os.MkdirAll(tmpdir, 0o0755); err != nil {
			return dir, fmt.Errorf("creating %s directory: %s", tmpdir, err)
		}
		// Mount the tmpfs
		err := unix.Mount("tmpfs", tmpdir, "tmpfs", uintptr(0), fmt.Sprintf("size=%d", sizeBytes))
		if err != nil {
			return dir, fmt.Errorf("tmpfs: %s", err)
		}
	}

	if err := os.MkdirAll(dir.work, 0o0755); err != nil {
		errs = errors.Join(err, fmt.Errorf("creating %s directory: %s", dir.work, err))
	}

	if err := os.MkdirAll(dir.upper, 0o0755); err != nil {
		errs = errors.Join(err, fmt.Errorf("creating %s directory: %s", dir.upper, err))
	}

	return dir, errs
}

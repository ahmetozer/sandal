package container

import (
	"fmt"
	"os"
	"path"
	"syscall"

	"github.com/ahmetozer/sandal/pkg/config"
)

type changesDir struct {
	uppper string
	work   string
}

func ChangeDir(c *config.Config) changesDir {
	if c.ChangeDir == "" {
		defaultChangeRoot := defaultChangeRoot(c)
		return changesDir{uppper: path.Join(defaultChangeRoot, "upper"), work: path.Join(defaultChangeRoot, "work")}
	}
	return changesDir{uppper: path.Join(c.ChangeDir, "upper"), work: path.Join(c.ChangeDir, "work")}
}

func defaultChangeRoot(c *config.Config) string {
	return path.Join(c.ContDir(), "changes")
}
func createChangeDir(c *config.Config) (changesDir, error) {
	dir := ChangeDir(c)
	if c.ChangeDir == "" && c.TmpSize != 0 {
		sizeBytes := uint64(c.TmpSize * 1024 * 1024) // 100MB
		if err := os.MkdirAll(defaultChangeRoot(c), 0755); err != nil {
			return dir, fmt.Errorf("creating %s directory: %s", defaultChangeRoot(c), err)
		}
		// Mount the tmpfs
		err := syscall.Mount("tmpfs", defaultChangeRoot(c), "tmpfs", uintptr(0), fmt.Sprintf("size=%d", sizeBytes))
		if err != nil {
			return dir, fmt.Errorf("tmpfs: %s", err)
		}
	}
	if err := os.MkdirAll(dir.work, 0755); err != nil {
		return dir, fmt.Errorf("creating %s directory: %s", dir.work, err)
	}
	if err := os.MkdirAll(dir.uppper, 0755); err != nil {
		return dir, fmt.Errorf("creating %s directory: %s", dir.uppper, err)
	}
	return dir, nil
}

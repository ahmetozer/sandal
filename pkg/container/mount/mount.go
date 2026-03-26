//go:build linux

package mount

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Mount wraps unix.Mount with path resolution and automatic directory/file creation.
// If source is an absolute path, its parent directory is created as needed, and
// if source is a regular file the target is touched before mounting.
func Mount(source, target, fstype string, flags uintptr, data string) error {
	slog.Debug("mount", slog.String("source", source), slog.String("target", target), slog.String("fstype", fstype))

	source = ResolvePath(source)

	// empty mount used for removing old root access from container
	if source != "" && source[0:1] == "/" {
		retried := false
	CHECK_FOLDER:
		_, err := os.Stat(filepath.Dir(source))
		if os.IsNotExist(err) {
			if !retried {
				os.MkdirAll(filepath.Dir(source), 0o0600)
				retried = true
				goto CHECK_FOLDER
			}
			return fmt.Errorf("path %s does not exist and unable to created", filepath.Dir(source))
		}

		if err != nil {
			return err
		}

		fileInfo, err := os.Stat(source)
		if os.IsNotExist(err) {
			return fmt.Errorf("path does not exist %s", filepath.Dir(source))
		}
		if err != nil {
			return err
		}

		if !fileInfo.IsDir() {
			os.MkdirAll(filepath.Dir(target), 0o0600)
			err = Touch(target)
			if err != nil {
				return fmt.Errorf("unable to touch, %v", err)
			}
		} else {
			os.MkdirAll(target, 0o0600)
		}
	} else {
		os.MkdirAll(target, 0o0600)
	}

	if err := unix.Mount(source, target, fstype, flags, data); err != nil {
		return err
	}
	return nil
}

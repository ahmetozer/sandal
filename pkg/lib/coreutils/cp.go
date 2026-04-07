// Package coreutils provides small filesystem helpers modelled loosely on
// the GNU coreutils commands of the same name.
package coreutils

import (
	"io"
	"os"
	"path/filepath"
)

// Cp copies the file at src to dst. The destination's parent directory is
// created if missing. On any error after dst is created, the partial
// destination file is removed so the caller does not have to clean up.
func Cp(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	return nil
}

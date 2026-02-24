//go:build linux

package modprobe

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// loadModuleFile loads a single kernel module file into the kernel.
// Supports .ko (uncompressed) and .ko.gz (gzip compressed).
// For uncompressed modules, uses FinitModule (fd-based).
// For compressed modules, decompresses into memory and uses InitModule.
func loadModuleFile(path string, params string) error {
	switch {
	case strings.HasSuffix(path, ".ko"):
		return loadUncompressed(path, params)
	case strings.HasSuffix(path, ".ko.gz"):
		return loadGzip(path, params)
	case strings.HasSuffix(path, ".ko.xz"):
		return fmt.Errorf("xz compressed modules not supported: %s", path)
	case strings.HasSuffix(path, ".ko.zst"):
		return fmt.Errorf("zstd compressed modules not supported: %s", path)
	default:
		// Try as uncompressed
		return loadUncompressed(path, params)
	}
}

func loadUncompressed(path string, params string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	err = unix.FinitModule(int(f.Fd()), params, 0)
	if err != nil && errors.Is(err, unix.EEXIST) {
		return nil
	}
	return err
}

func loadGzip(path string, params string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip open: %w", err)
	}
	defer gz.Close()

	data, err := io.ReadAll(gz)
	if err != nil {
		return fmt.Errorf("gzip read: %w", err)
	}

	err = unix.InitModule(data, params)
	if err != nil && errors.Is(err, unix.EEXIST) {
		return nil
	}
	return err
}

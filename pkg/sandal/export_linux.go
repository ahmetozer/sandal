//go:build linux

package sandal

import (
	"fmt"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
)

func exportNative(c *config.Config, outputPath string, includes, excludes []string) (string, error) {
	rootfsDir := c.RootfsDir
	if _, err := os.Stat(rootfsDir); err != nil {
		return "", fmt.Errorf("rootfs not found (is the container running?): %w", err)
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return "", fmt.Errorf("create output: %w", err)
	}
	defer outFile.Close()

	var opts []squashfs.WriterOption
	if len(includes) > 0 || len(excludes) > 0 {
		inc := includes
		if len(inc) == 0 {
			inc = []string{"/"}
		}
		opts = append(opts, squashfs.WithPathFilter(
			squashfs.NewIncludeExcludeFilter(inc, excludes),
		))
	}

	w, err := squashfs.NewWriter(outFile, opts...)
	if err != nil {
		os.Remove(outputPath)
		return "", fmt.Errorf("squashfs writer: %w", err)
	}

	if err := w.CreateFromDir(rootfsDir); err != nil {
		os.Remove(outputPath)
		return "", fmt.Errorf("export failed: %w", err)
	}

	return outputPath, nil
}

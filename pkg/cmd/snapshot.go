//go:build linux

package cmd

import (
	"flag"
	"fmt"
	"os"
	"path"

	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
)

func Snapshot(args []string) error {
	flags := flag.NewFlagSet("snapshot", flag.ExitOnError)
	var (
		help     bool
		filePath string
	)

	flags.BoolVar(&help, "help", false, "show this help message")
	flags.StringVar(&filePath, "f", "", "custom output file path (default: SANDAL_SNAPSHOT_DIR/<container>.sqfs)")
	flags.Parse(args)

	if help || len(flags.Args()) < 1 {
		fmt.Printf("Usage: %s snapshot [OPTIONS] CONTAINER\n\nSnapshot container changes (upper workdir) as a squashfs image.\n\nOPTIONS:\n", os.Args[0])
		flags.PrintDefaults()
		return nil
	}

	containerName := flags.Args()[0]

	c, err := controller.GetContainer(containerName)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", containerName, err)
	}

	upperDir := path.Join(c.ChangeDir, "upper")
	if _, err := os.Stat(upperDir); err != nil {
		return fmt.Errorf("change directory not found: %w", err)
	}

	if filePath == "" {
		if err := os.MkdirAll(env.BaseSnapshotDir, 0o755); err != nil {
			return fmt.Errorf("creating snapshot directory: %w", err)
		}
		filePath = path.Join(env.BaseSnapshotDir, containerName+".sqfs")
	}

	outFile, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer outFile.Close()

	w, err := squashfs.NewWriter(outFile)
	if err != nil {
		os.Remove(filePath)
		return fmt.Errorf("creating squashfs writer: %w", err)
	}

	if err := w.CreateFromDir(upperDir); err != nil {
		os.Remove(filePath)
		return fmt.Errorf("creating squashfs image: %w", err)
	}

	fmt.Printf("%s\n", filePath)
	return nil
}

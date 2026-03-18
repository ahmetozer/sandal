//go:build linux

package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
)

func Export(args []string) error {
	flags := flag.NewFlagSet("export", flag.ExitOnError)
	var help bool
	var fromDir string

	flags.BoolVar(&help, "help", false, "show this help message")
	flags.StringVar(&fromDir, "from", "", "create squashfs from a custom directory instead of a container")
	flags.Parse(args)

	if fromDir != "" {
		if help || len(flags.Args()) < 1 {
			fmt.Printf("Usage: %s export -from DIR OUTPUT.sqfs\n\nExport a custom directory as a squashfs image.\n\nOPTIONS:\n", os.Args[0])
			flags.PrintDefaults()
			return nil
		}
		outputPath := flags.Args()[0]

		if _, err := os.Stat(fromDir); err != nil {
			return fmt.Errorf("source directory not found: %w", err)
		}

		return createSquashfs(fromDir, outputPath)
	}

	if help || len(flags.Args()) < 2 {
		fmt.Printf("Usage: %s export CONTAINER OUTPUT.sqfs\n       %s export -from DIR OUTPUT.sqfs\n\nExport a container or custom directory as a squashfs image.\n\nOPTIONS:\n", os.Args[0], os.Args[0])
		flags.PrintDefaults()
		return nil
	}

	leftArgs := flags.Args()
	containerName := leftArgs[0]
	outputPath := leftArgs[1]

	c, err := controller.GetContainer(containerName)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", containerName, err)
	}

	rootfsDir := c.RootfsDir
	if _, err := os.Stat(rootfsDir); err != nil {
		return fmt.Errorf("rootfs directory not found (is the container running?): %w", err)
	}

	return createSquashfs(rootfsDir, outputPath)
}

func createSquashfs(sourceDir, outputPath string) error {
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer outFile.Close()

	w, err := squashfs.NewWriter(outFile)
	if err != nil {
		os.Remove(outputPath)
		return fmt.Errorf("creating squashfs writer: %w", err)
	}

	if err := w.CreateFromDir(sourceDir); err != nil {
		os.Remove(outputPath)
		return fmt.Errorf("creating squashfs image: %w", err)
	}

	fmt.Printf("%s\n", outputPath)
	return nil
}

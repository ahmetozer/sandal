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

	flags.BoolVar(&help, "help", false, "show this help message")
	flags.Parse(args)

	if help || len(flags.Args()) < 2 {
		fmt.Printf("Usage: %s export CONTAINER OUTPUT.sqfs\n\nExport the full container filesystem as a squashfs image.\n\nOPTIONS:\n", os.Args[0])
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

	if err := w.CreateFromDir(rootfsDir); err != nil {
		os.Remove(outputPath)
		return fmt.Errorf("creating squashfs image: %w", err)
	}

	fmt.Printf("%s\n", outputPath)
	return nil
}

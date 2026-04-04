//go:build linux || darwin

package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/config/wrapper"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/sandal"
)

func Snapshot(args []string) error {
	flags := flag.NewFlagSet("snapshot", flag.ExitOnError)
	var (
		help     bool
		filePath string
		includes wrapper.StringFlags
		excludes wrapper.StringFlags
	)

	flags.BoolVar(&help, "help", false, "show this help message")
	flags.StringVar(&filePath, "f", "", "custom output file path (default: SANDAL_SNAPSHOT_DIR/<container>.sqfs)")
	flags.Var(&includes, "i", "include path (can be specified multiple times)")
	flags.Var(&excludes, "e", "exclude path (can be specified multiple times)")
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

	outPath, err := sandal.Snapshot(c, filePath, []string(includes), []string(excludes))
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", outPath)
	return nil
}

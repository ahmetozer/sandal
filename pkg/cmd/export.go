//go:build linux || darwin

package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/config/wrapper"
	"github.com/ahmetozer/sandal/pkg/sandal"
)

func Export(args []string) error {
	flags := flag.NewFlagSet("export", flag.ExitOnError)
	var (
		help       bool
		fromDir    string
		imageRef   string
		tarGz      bool
		outputPath string
		includes   wrapper.StringFlags
		excludes   wrapper.StringFlags
	)

	flags.BoolVar(&help, "help", false, "show this help message")
	flags.StringVar(&fromDir, "from", "", "create squashfs from a custom directory instead of a container")
	flags.StringVar(&imageRef, "image", "", "export a container image from a registry")
	flags.BoolVar(&tarGz, "targz", false, "export as tar.gz instead of squashfs (only with -image)")
	flags.StringVar(&outputPath, "o", "", "output file path")
	flags.Var(&includes, "i", "include path (can be specified multiple times)")
	flags.Var(&excludes, "e", "exclude path (can be specified multiple times)")
	flags.Parse(args)

	if imageRef != "" {
		if help || outputPath == "" {
			fmt.Printf("Usage: %s export -image IMAGE -o OUTPUT.sqfs\n       %s export -image IMAGE -targz -o OUTPUT.tar.gz\n\nExport a container image from a registry.\n\nOPTIONS:\n", os.Args[0], os.Args[0])
			flags.PrintDefaults()
			return nil
		}
	} else if fromDir != "" {
		if help || (outputPath == "" && len(flags.Args()) < 1) {
			fmt.Printf("Usage: %s export -from DIR OUTPUT.sqfs\n\nExport a custom directory as a squashfs image.\n\nOPTIONS:\n", os.Args[0])
			flags.PrintDefaults()
			return nil
		}
		if outputPath == "" {
			outputPath = flags.Args()[0]
		}
	} else {
		if help || (outputPath == "" && len(flags.Args()) < 2) {
			fmt.Printf("Usage: %s export CONTAINER OUTPUT.sqfs\n       %s export -from DIR OUTPUT.sqfs\n       %s export -image IMAGE -o OUTPUT.sqfs\n\nExport a container, directory, or registry image as a squashfs image.\n\nOPTIONS:\n", os.Args[0], os.Args[0], os.Args[0])
			flags.PrintDefaults()
			return nil
		}
	}

	var containerName string
	if imageRef == "" && fromDir == "" {
		containerName = flags.Args()[0]
		if outputPath == "" {
			outputPath = flags.Args()[1]
		}
	}

	outPath, err := sandal.Export(sandal.ExportArgs{
		ContainerName: containerName,
		FromDir:       fromDir,
		ImageRef:      imageRef,
		TarGz:         tarGz,
		OutputPath:    outputPath,
		Includes:      []string(includes),
		Excludes:      []string(excludes),
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", outPath)
	return nil
}

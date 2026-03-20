//go:build linux

package cmd

import (
	"compress/gzip"
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/ahmetozer/sandal/pkg/container/config/wrapper"
	"github.com/ahmetozer/sandal/pkg/controller"
	squash "github.com/ahmetozer/sandal/pkg/lib/container/image"
	"github.com/ahmetozer/sandal/pkg/lib/squashfs"
)

func Export(args []string) error {
	flags := flag.NewFlagSet("export", flag.ExitOnError)
	var help bool
	var fromDir string
	var imageRef string
	var tarGz bool
	var outputPath string
	var includes, excludes wrapper.StringFlags

	flags.BoolVar(&help, "help", false, "show this help message")
	flags.StringVar(&fromDir, "from", "", "create squashfs from a custom directory instead of a container")
	flags.StringVar(&imageRef, "image", "", "export a container image from a registry")
	flags.BoolVar(&tarGz, "targz", false, "export as tar.gz instead of squashfs (only with -image)")
	flags.StringVar(&outputPath, "o", "", "output file path")
	flags.Var(&includes, "i", "include path (can be specified multiple times)")
	flags.Var(&excludes, "e", "exclude path (can be specified multiple times)")
	flags.Parse(args)

	// Image export mode: sandal export -image <ref> -o output.sqfs
	//                     sandal export -image <ref> -targz -o output.tar.gz
	if imageRef != "" {
		if help || outputPath == "" {
			fmt.Printf("Usage: %s export -image IMAGE -o OUTPUT.sqfs\n       %s export -image IMAGE -targz -o OUTPUT.tar.gz\n\nExport a container image from a registry.\n\nOPTIONS:\n", os.Args[0], os.Args[0])
			flags.PrintDefaults()
			return nil
		}
		return exportImage(imageRef, outputPath, tarGz)
	}

	if fromDir != "" {
		if help || (outputPath == "" && len(flags.Args()) < 1) {
			fmt.Printf("Usage: %s export -from DIR OUTPUT.sqfs\n\nExport a custom directory as a squashfs image.\n\nOPTIONS:\n", os.Args[0])
			flags.PrintDefaults()
			return nil
		}
		if outputPath == "" {
			outputPath = flags.Args()[0]
		}

		if _, err := os.Stat(fromDir); err != nil {
			return fmt.Errorf("source directory not found: %w", err)
		}

		return createSquashfs(fromDir, outputPath, includes, excludes)
	}

	if help || (outputPath == "" && len(flags.Args()) < 2) {
		fmt.Printf("Usage: %s export CONTAINER OUTPUT.sqfs\n       %s export -from DIR OUTPUT.sqfs\n       %s export -image IMAGE -o OUTPUT.sqfs\n\nExport a container, directory, or registry image as a squashfs image.\n\nOPTIONS:\n", os.Args[0], os.Args[0], os.Args[0])
		flags.PrintDefaults()
		return nil
	}

	leftArgs := flags.Args()
	containerName := leftArgs[0]
	if outputPath == "" {
		outputPath = leftArgs[1]
	}

	c, err := controller.GetContainer(containerName)
	if err != nil {
		return fmt.Errorf("container %q not found: %w", containerName, err)
	}

	rootfsDir := c.RootfsDir
	if _, err := os.Stat(rootfsDir); err != nil {
		return fmt.Errorf("rootfs directory not found (is the container running?): %w", err)
	}

	return createSquashfs(rootfsDir, outputPath, includes, excludes)
}

func exportImage(imageRef, outputPath string, tarGz bool) error {
	ctx := context.Background()

	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer outFile.Close()

	if tarGz {
		gw := gzip.NewWriter(outFile)
		defer gw.Close()
		if err := squash.ExportImageTarGz(ctx, imageRef, gw); err != nil {
			os.Remove(outputPath)
			return err
		}
		fmt.Printf("%s\n", outputPath)
		return nil
	}

	if err := squash.ExportImageSquashfs(ctx, imageRef, outFile); err != nil {
		os.Remove(outputPath)
		return err
	}
	fmt.Printf("%s\n", outputPath)
	return nil
}

func createSquashfs(sourceDir, outputPath string, includes, excludes []string) error {
	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
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
		return fmt.Errorf("creating squashfs writer: %w", err)
	}

	if err := w.CreateFromDir(sourceDir); err != nil {
		os.Remove(outputPath)
		return fmt.Errorf("creating squashfs image: %w", err)
	}

	fmt.Printf("%s\n", outputPath)
	return nil
}

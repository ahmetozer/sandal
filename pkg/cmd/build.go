//go:build linux || darwin

package cmd

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ahmetozer/sandal/pkg/container/config/wrapper"
	"github.com/ahmetozer/sandal/pkg/sandal"
)

// Build implements the `sandal build` subcommand.
//
// Usage: sandal build [OPTIONS] CONTEXT
//
//   sandal build -t myimg:latest .
//   sandal build -t myimg:latest -f Dockerfile.dev .
//   sandal build -t reg.example.com/foo:1 --push .
func Build(args []string) error {
	flags := flag.NewFlagSet("build", flag.ExitOnError)

	var (
		help       bool
		tag        string
		dockerfile string
		push       bool
		target     string
		buildArgs  wrapper.StringFlags
		dryRun     bool
		useVM      bool
		cpuLimit   string
		memory     string
		tmpSize    uint
		csize      string
	)

	flags.BoolVar(&help, "help", false, "show this help message")
	flags.StringVar(&tag, "t", "", "image tag (e.g. name:latest or registry/name:tag)")
	flags.StringVar(&dockerfile, "f", "", "Dockerfile path (default: <CONTEXT>/Dockerfile)")
	flags.BoolVar(&push, "push", false, "push image to registry after build")
	flags.StringVar(&target, "target", "", "build only up to the named stage (multi-stage)")
	flags.Var(&buildArgs, "build-arg", "build-time variable KEY=VALUE (repeatable)")
	flags.BoolVar(&dryRun, "dry-run", false, "parse Dockerfile and print the plan only")
	flags.BoolVar(&useVM, "vm", false, "run the build inside a virtual machine (required on macOS)")
	flags.StringVar(&cpuLimit, "cpu", "", "CPUs for the build VM (e.g. 0.5, 2) — only used with -vm")
	flags.StringVar(&memory, "memory", "", "memory for the build VM (e.g. 512M, 1G) — only used with -vm")
	flags.UintVar(&tmpSize, "tmp", 0, "back stage rootfs and RUN change dirs with a tmpfs of the given size in MB (default: use disk)")
	flags.StringVar(&csize, "csize", "", "disk image size for ext4-backed build (e.g. 4g, 8g). Default 8g.")
	flags.Parse(args)

	if help || len(flags.Args()) < 1 {
		fmt.Printf("Usage: %s build [OPTIONS] CONTEXT\n\nBuild an OCI image from a Dockerfile.\n\nOPTIONS:\n", os.Args[0])
		flags.PrintDefaults()
		return nil
	}

	contextDir := flags.Args()[0]
	if tag == "" && !dryRun {
		return fmt.Errorf("-t TAG is required (use --dry-run to parse without building)")
	}

	parsedArgs := map[string]string{}
	for _, kv := range buildArgs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("--build-arg %q must be KEY=VALUE", kv)
		}
		parsedArgs[k] = v
	}

	opts := sandal.BuildOpts{
		ContextDir:     contextDir,
		DockerfilePath: dockerfile,
		Tag:            tag,
		Push:           push,
		Target:         target,
		BuildArgs:      parsedArgs,
		DryRun:         dryRun,
		VM:             useVM,
		CPULimit:       cpuLimit,
		MemoryLimit:    memory,
		TmpSize:        tmpSize,
		ChangeDirSize:  csize,
	}

	out, err := sandal.Build(opts)
	if err != nil {
		return err
	}
	if out != "" {
		fmt.Println(out)
	}
	return nil
}

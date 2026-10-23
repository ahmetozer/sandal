//go:build linux || darwin

package sandal

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/lib/container/build"
)

// BuildOpts collects flags from `sandal build` for delegation to the
// platform-specific builder.
type BuildOpts struct {
	ContextDir     string
	DockerfilePath string
	Tag            string
	Push           bool
	Target         string
	BuildArgs      map[string]string
	DryRun         bool
	VM             bool   // run build inside a VM (required on darwin, optional on linux)
	CPULimit       string // CPUs for build VM (e.g. "0.5", "2") — VM mode only
	MemoryLimit    string // memory for build VM (e.g. "512M", "1G") — VM mode only

	// Backing storage for stage rootfs and per-RUN change dirs:
	//   TmpSize > 0          → tmpfs of that size (MB) — fast, RAM-limited
	//   TmpSize == 0         → folder mode when the host fs supports nested
	//                          overlayfs; otherwise ext4 loop image sized
	//                          by ChangeDirSize.
	TmpSize       uint   // MB; 0 disables tmpfs
	ChangeDirSize string // e.g. "4g", "8g" — only used for image-backed mode
}

// Build orchestrates a Dockerfile-based image build.
//
// On linux it dispatches to the real builder. On darwin it currently
// reports "unsupported" — VM-mode build will be added later.
func Build(opts BuildOpts) (string, error) {
	if opts.ContextDir == "" {
		return "", fmt.Errorf("CONTEXT directory is required")
	}

	dfPath := opts.DockerfilePath
	if dfPath == "" {
		dfPath = filepath.Join(opts.ContextDir, "Dockerfile")
	}
	if !filepath.IsAbs(dfPath) {
		// Resolve relative to CWD, not context (matches docker build).
		abs, err := filepath.Abs(dfPath)
		if err != nil {
			return "", fmt.Errorf("resolving dockerfile path: %w", err)
		}
		dfPath = abs
	}

	df, err := os.Open(dfPath)
	if err != nil {
		return "", fmt.Errorf("opening Dockerfile: %w", err)
	}
	defer df.Close()

	instrs, err := build.ParseDockerfile(df)
	if err != nil {
		return "", fmt.Errorf("parsing Dockerfile: %w", err)
	}

	globalArgs, stages, err := build.SplitStages(instrs)
	if err != nil {
		return "", fmt.Errorf("splitting stages: %w", err)
	}

	if opts.DryRun {
		printPlan(globalArgs, stages)
		return "", nil
	}

	return runBuild(opts, dfPath, globalArgs, stages)
}

func printPlan(globalArgs []build.Instruction, stages []*build.Stage) {
	if len(globalArgs) > 0 {
		fmt.Println("Global ARGs:")
		for _, a := range globalArgs {
			fmt.Printf("  %s\n", a.Raw)
		}
	}
	for i, s := range stages {
		name := s.Name
		if name == "" {
			name = fmt.Sprintf("(stage %d)", i)
		}
		fmt.Printf("Stage %d %s — FROM %s\n", i, name, s.BaseRef)
		for _, in := range s.Instrs {
			fmt.Printf("  L%d %s\n", in.Line, in.Raw)
		}
	}
}

// checkGuestBuildOutput reports whether a VM build produced a new image at
// outPath. The guest's exit status does not reach the host (the VM simply
// powers off), so the output file is the only signal. before is the
// os.Stat of outPath taken before the VM booted (nil if there was none):
// the guest writes a new file and renames it over outPath, so an image
// that is still the same file means the build failed and left an older
// image with the same tag in place.
func checkGuestBuildOutput(outPath string, before os.FileInfo) error {
	after, err := os.Stat(outPath)
	if err != nil {
		return fmt.Errorf("guest build failed: no image written to %s: %w", outPath, err)
	}
	if before != nil && os.SameFile(before, after) {
		return fmt.Errorf("guest build failed: %s was not rebuilt (kept the previous image)", outPath)
	}
	return nil
}

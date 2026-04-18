//go:build darwin

package sandal

import (
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ahmetozer/sandal/pkg/env"
	squash "github.com/ahmetozer/sandal/pkg/lib/container/image"
	"github.com/ahmetozer/sandal/pkg/lib/wordgenerator"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kernel"
	"github.com/ahmetozer/sandal/pkg/vm/vz"
)

// buildInVZ boots a VZ VM on macOS that runs `sandal build` with the
// given opts inside the guest. It is the darwin counterpart of
// buildInKVM: same kernel-cmdline transport, same VirtioFS sharing.
func buildInVZ(opts BuildOpts) (string, error) {
	absCtx, err := filepath.Abs(opts.ContextDir)
	if err != nil {
		return "", fmt.Errorf("abs context: %w", err)
	}
	st, err := os.Stat(absCtx)
	if err != nil || !st.IsDir() {
		return "", fmt.Errorf("context %s is not a directory", absCtx)
	}

	dockerfilePath := opts.DockerfilePath
	if dockerfilePath == "" {
		dockerfilePath = filepath.Join(absCtx, "Dockerfile")
	}
	absDockerfile, err := filepath.Abs(dockerfilePath)
	if err != nil {
		return "", fmt.Errorf("abs dockerfile: %w", err)
	}

	hostPaths := []string{absCtx}
	if !pathUnder(absDockerfile, absCtx) {
		hostPaths = append(hostPaths, absDockerfile)
	}

	guestCtx := filepath.Join("/mnt", absCtx)
	guestDockerfile := filepath.Join("/mnt", absDockerfile)

	buildArgs := []string{"-t", opts.Tag}
	if opts.DockerfilePath != "" {
		buildArgs = append(buildArgs, "-f", guestDockerfile)
	}
	if opts.Push {
		buildArgs = append(buildArgs, "--push")
	}
	if opts.Target != "" {
		buildArgs = append(buildArgs, "--target", opts.Target)
	}
	if opts.TmpSize > 0 {
		buildArgs = append(buildArgs, "-tmp", fmt.Sprintf("%d", opts.TmpSize))
	}
	if opts.ChangeDirSize != "" {
		buildArgs = append(buildArgs, "-csize", opts.ChangeDirSize)
	}
	for k, v := range opts.BuildArgs {
		buildArgs = append(buildArgs, "--build-arg", k+"="+v)
	}
	buildArgs = append(buildArgs, guestCtx)

	argsJSON, err := MarshalVMSubcommandArgs("build", buildArgs)
	if err != nil {
		return "", err
	}

	cfg := vmconfig.VMConfig{
		CPUCount:    vmconfig.DefaultCPUCount,
		MemoryBytes: vmconfig.DefaultBuildMemoryMB * vmconfig.MB,
	}
	if opts.CPULimit != "" {
		if n, err := strconv.ParseFloat(opts.CPULimit, 64); err == nil && n > 0 {
			cfg.CPUCount = uint(math.Ceil(n))
		}
	}
	if opts.MemoryLimit != "" {
		if n, err := parseMemSize(opts.MemoryLimit); err == nil && n > 0 {
			// VZ requires memory to be a multiple of 1 MB.
			cfg.MemoryBytes = (n + vmconfig.MB - 1) / vmconfig.MB * vmconfig.MB
		}
	}

	kernelPath, err := kernel.EnsureKernel()
	if err != nil {
		return "", fmt.Errorf("auto-downloading kernel: %w", err)
	}
	cfg.KernelPath = kernelPath

	mounts, mountEntries, err := BuildVirtioFSMounts(hostPaths, env.LibDir)
	if err != nil {
		return "", err
	}
	cfg.Mounts = append(cfg.Mounts, mounts...)

	// macOS /etc/resolv.conf is empty; DNS is served by mDNSResponder.
	// Stage a generated resolv.conf so the guest can reach OCI registries.
	etcDir := stageHostEtc()
	cfg.Mounts = append(cfg.Mounts, vmconfig.MountConfig{
		Tag:      "host-etc",
		HostPath: etcDir,
		ReadOnly: true,
	})
	mountEntries = append(mountEntries, "host-etc="+etcDir+"=/mnt/host-etc")

	// No network allocation on darwin (VZ NAT is automatic).
	cfg.CommandLine = BuildKernelCmdLine("mac", argsJSON, mountEntries, "", nil)

	initrdPath, err := PrepareInitrd(cfg.KernelPath)
	if err != nil {
		return "", err
	}
	cfg.InitrdPath = initrdPath

	vmName := "sandal-build-vm-" + strings.Join(wordgenerator.NameGenerate(4), "-")
	if err := vmconfig.SaveConfig(vmName, cfg); err != nil {
		return "", fmt.Errorf("saving ephemeral VM config: %w", err)
	}
	defer vmconfig.DeleteVM(vmName)

	slog.Info("buildInVZ", "action", "booting", "tag", opts.Tag, "context", absCtx)
	if err := vz.Boot(vmName, cfg); err != nil {
		return "", fmt.Errorf("vm boot: %w", err)
	}

	// Guest wrote the squashfs (and .json sidecar) to the shared
	// sandal-lib image cache. Derive the host path so callers can find
	// the built image without parsing guest logs.
	outPath := filepath.Join(env.BaseImageDir, squash.SanitizeRef(opts.Tag)+".sqfs")
	if _, err := os.Stat(outPath); err != nil {
		return "", fmt.Errorf("guest build completed but image not found at %s: %w", outPath, err)
	}
	return outPath, nil
}

// parseMemSize parses a memory size string like "512M", "1G", "1Gi" into bytes.
// Accepts K/M/G/T (1000-based) and Ki/Mi/Gi/Ti (1024-based). A bare number
// is treated as bytes. Case-insensitive.
func parseMemSize(s string) (uint64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	up := strings.ToUpper(strings.TrimSpace(s))
	mult := uint64(1)
	switch {
	case strings.HasSuffix(up, "KI"):
		mult = 1024
		up = up[:len(up)-2]
	case strings.HasSuffix(up, "MI"):
		mult = 1024 * 1024
		up = up[:len(up)-2]
	case strings.HasSuffix(up, "GI"):
		mult = 1024 * 1024 * 1024
		up = up[:len(up)-2]
	case strings.HasSuffix(up, "TI"):
		mult = 1024 * 1024 * 1024 * 1024
		up = up[:len(up)-2]
	case strings.HasSuffix(up, "K"), strings.HasSuffix(up, "KB"):
		mult = 1000
		up = strings.TrimSuffix(strings.TrimSuffix(up, "B"), "K")
	case strings.HasSuffix(up, "M"), strings.HasSuffix(up, "MB"):
		mult = 1000 * 1000
		up = strings.TrimSuffix(strings.TrimSuffix(up, "B"), "M")
	case strings.HasSuffix(up, "G"), strings.HasSuffix(up, "GB"):
		mult = 1000 * 1000 * 1000
		up = strings.TrimSuffix(strings.TrimSuffix(up, "B"), "G")
	case strings.HasSuffix(up, "T"), strings.HasSuffix(up, "TB"):
		mult = 1000 * 1000 * 1000 * 1000
		up = strings.TrimSuffix(strings.TrimSuffix(up, "B"), "T")
	}
	n, err := strconv.ParseUint(strings.TrimSpace(up), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse size %q: %w", s, err)
	}
	return n * mult, nil
}

// pathUnder reports whether child is inside parent (both must be absolute).
func pathUnder(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if len(rel) >= 2 && rel[0] == '.' && rel[1] == '.' {
		return false
	}
	return true
}

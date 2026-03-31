//go:build darwin

package run

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/env"
	squash "github.com/ahmetozer/sandal/pkg/lib/container/image"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kernel"
	"github.com/ahmetozer/sandal/pkg/vm/vz"
)

func Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no command option provided")
	}

	// Extract -vm flag (darwin-only, not forwarded to VM).
	// If specified, load that config as a base template; otherwise use defaults.
	vmFlag, cleanArgs := extractFlag(args, "vm", "")

	// Scan args for -v values to determine VirtioFS shares.
	hostPaths, _ := scanMountPaths(cleanArgs)

	// Build VM config: load from template if -vm was specified, otherwise use defaults
	var cfg vmconfig.VMConfig
	if vmFlag != "" {
		var err error
		cfg, err = vmconfig.LoadConfig(vmFlag)
		if err != nil {
			return fmt.Errorf("loading VM config '%s': %w", vmFlag, err)
		}
	} else {
		cfg = vmconfig.VMConfig{
			CPUCount:    vmconfig.DefaultCPUCount,
			MemoryBytes: vmconfig.DefaultMemoryMB * vmconfig.MB,
		}
	}

	// Auto-download kernel if not configured or missing
	if cfg.KernelPath == "" {
		p, err := kernel.EnsureKernel()
		if err != nil {
			return fmt.Errorf("auto-downloading kernel: %w", err)
		}
		cfg.KernelPath = p
	} else if _, err := os.Stat(cfg.KernelPath); err != nil {
		p, err := kernel.EnsureKernel()
		if err != nil {
			return fmt.Errorf("kernel %s not found and auto-download failed: %w", cfg.KernelPath, err)
		}
		cfg.KernelPath = p
	}

	// Resolve Linux sandal binary (configured via SANDAL_VM_BIN env var)
	if _, err := os.Stat(env.VMBinPath); err != nil {
		return fmt.Errorf("Linux sandal binary not found at %s (cross-compile with: GOOS=linux CGO_ENABLED=0 go build -o %s .)", env.VMBinPath, env.VMBinPath)
	}

	// Pre-pull OCI images on the host and convert to squashfs.
	home, _ := os.UserHomeDir()
	sandalLibDir := filepath.Join(home, ".sandal", "lib")
	imageDir := filepath.Join(sandalLibDir, "image")
	cleanArgs = squash.PullFromArgs(cleanArgs, imageDir)

	// Build VirtioFS mounts from collected host paths.
	mounts, mountEntries, err := buildVirtioFSMounts(hostPaths, sandalLibDir)
	if err != nil {
		return err
	}
	cfg.Mounts = append(cfg.Mounts, mounts...)

	// Marshal args for the kernel command line
	argsJSON, err := marshalVMArgs(cleanArgs)
	if err != nil {
		return err
	}

	// Build kernel command line (no network allocation on darwin)
	cfg.CommandLine = buildKernelCmdLine("mac", argsJSON, mountEntries, "", nil)

	// Create initrd with sandal binary as /init
	initrdPath, err := prepareInitrd(cfg.KernelPath, env.VMBinPath)
	if err != nil {
		return err
	}
	defer os.Remove(initrdPath)
	cfg.InitrdPath = initrdPath

	// Each run gets an ephemeral VM that is cleaned up on exit.
	vmName := fmt.Sprintf("run-%d", os.Getpid())
	if err := vmconfig.SaveConfig(vmName, cfg); err != nil {
		return fmt.Errorf("saving ephemeral VM config: %w", err)
	}
	defer vmconfig.DeleteVM(vmName)

	return vz.Boot(vmName, cfg)
}

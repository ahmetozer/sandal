//go:build darwin

package sandal

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

// RunInVZ boots a VZ VM on macOS with the sandal Linux binary as /init,
// then re-executes `sandal run` inside the VM with the original args.
func RunInVZ(args []string) error {
	// Extract -vm flag (darwin-only, not forwarded to VM).
	vmFlag, cleanArgs := ExtractFlag(args, "vm", "")

	// Scan args for -v values to determine VirtioFS shares and socket mounts.
	hostPaths, socketMounts := ScanMountPaths(cleanArgs)

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

	// Resolve Linux sandal binary
	if _, err := os.Stat(env.VMBinPath); err != nil {
		return fmt.Errorf("Linux sandal binary not found at %s (cross-compile with: GOOS=linux CGO_ENABLED=0 go build -o %s .)", env.VMBinPath, env.VMBinPath)
	}

	// Pre-pull OCI images on the host and convert to squashfs.
	home, _ := os.UserHomeDir()
	sandalLibDir := filepath.Join(home, ".sandal", "lib")
	imageDir := filepath.Join(sandalLibDir, "image")
	cleanArgs = squash.PullFromArgs(cleanArgs, imageDir)

	// Build VirtioFS mounts from collected host paths.
	mounts, mountEntries, err := BuildVirtioFSMounts(hostPaths, sandalLibDir)
	if err != nil {
		return err
	}
	cfg.Mounts = append(cfg.Mounts, mounts...)

	// Build socket relay entries for SANDAL_VM_SOCKETS
	var socketEntries []string
	var vsockRelays []vz.SocketRelay
	for i, sm := range socketMounts {
		port := uint32(5000 + i)
		socketEntries = append(socketEntries, fmt.Sprintf("%d=%s=%s", port, sm.HostPath, sm.GuestPath))
		vsockRelays = append(vsockRelays, vz.SocketRelay{Port: port, HostPath: sm.HostPath})
	}

	// Rewrite socket -v entries
	if len(socketMounts) > 0 {
		cleanArgs = RewriteSocketVolumes(cleanArgs, socketMounts)
	}

	// Marshal args for the kernel command line
	argsJSON, err := MarshalVMArgs(cleanArgs)
	if err != nil {
		return err
	}

	// Build kernel command line (no network allocation on darwin)
	cfg.CommandLine = BuildKernelCmdLine("mac", argsJSON, mountEntries, "", socketEntries)

	// Create initrd with sandal binary as /init
	initrdPath, err := PrepareInitrd(cfg.KernelPath, env.VMBinPath)
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

	return vz.Boot(vmName, cfg, vsockRelays...)
}

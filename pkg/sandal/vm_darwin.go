//go:build darwin

package sandal

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	squash "github.com/ahmetozer/sandal/pkg/lib/container/image"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kernel"
	"github.com/ahmetozer/sandal/pkg/vm/vz"
)

// RunInVZ boots a VZ VM on macOS with the sandal Linux binary as /init,
// then re-executes `sandal run` inside the VM with the original args.
func RunInVZ(c *config.Config) error {
	// Build args from HostArgs, stripping binary name and "run"
	var rawArgs []string
	if len(c.HostArgs) > 2 {
		rawArgs = c.HostArgs[2:]
	}

	// Remove -vm flag from args -- it's consumed here, not forwarded to VM.
	cleanArgs := RemoveBoolFlag(rawArgs, "vm")

	// Scan args for -v values to determine VirtioFS shares and socket mounts.
	hostPaths, socketMounts := ScanMountPaths(cleanArgs)

	// Build VM config: try loading a named config for this container, fall back to defaults
	cfg, err := vmconfig.LoadConfig(c.Name)
	if err != nil {
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

	// Share host /etc read-only so the VM can access resolv.conf, hosts, etc.
	cfg.Mounts = append(cfg.Mounts, vmconfig.MountConfig{
		Tag:      "host-etc",
		HostPath: "/etc",
		ReadOnly: true,
	})
	mountEntries = append(mountEntries, "host-etc=/etc=/mnt/host-etc")

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
	vmName := c.Name
	if err := vmconfig.SaveConfig(vmName, cfg); err != nil {
		return fmt.Errorf("saving ephemeral VM config: %w", err)
	}
	defer vmconfig.DeleteVM(vmName)

	// Register container in state files (mirror RunInKVM behavior)
	c.HostPid = os.Getpid()
	c.VM = "vz"
	c.Status = "running"
	if err := controller.SetContainer(c); err != nil {
		slog.Warn("RunInVZ", slog.String("action", "register container"), slog.Any("error", err))
	}
	defer func() {
		if c.Remove {
			controller.DeleteContainer(c.Name)
			os.Remove(c.ConfigFileLoc())
		}
	}()

	err = vz.Boot(vmName, cfg, vsockRelays...)

	// Update status after VM exits
	if err != nil {
		c.Status = fmt.Sprintf("err %v", err)
	} else {
		c.Status = "exit 0"
	}
	controller.SetContainer(c)

	return err
}

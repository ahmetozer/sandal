//go:build darwin

package sandal

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/ahmetozer/sandal/pkg/container/config"
	crt "github.com/ahmetozer/sandal/pkg/container/runtime"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	squash "github.com/ahmetozer/sandal/pkg/lib/container/image"
	"github.com/ahmetozer/sandal/pkg/lib/progress"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kernel"
	"github.com/ahmetozer/sandal/pkg/vm/vz"
)

// stageHostEtc creates a staging directory with host /etc files
// that the VM needs. On macOS, /etc is a symlink to /private/etc
// which VirtioFS may not follow, so we stage the files explicitly.
func stageHostEtc() string {
	dir := filepath.Join(env.LibDir, "system", "etc")
	os.MkdirAll(dir, 0755)

	resolvPath := filepath.Join(dir, "resolv.conf")
	if _, err := os.Stat(resolvPath); err != nil {
		if data, err := os.ReadFile("/etc/resolv.conf"); err == nil {
			os.WriteFile(resolvPath, data, 0644)
		}
	}
	return dir
}

// RunInVZ boots a VZ VM on macOS with the sandal Linux binary as /init,
// then re-executes `sandal run` inside the VM with the original args.
//
// netFlags carries the user's `-net` flags. On darwin, VZ currently only
// attaches a single NAT'd virtio-net device (see pkg/vm/vz/vm.go), so
// multiple `-net` flags are rejected here rather than silently hanging
// inside the guest waiting for an eth1 that does not exist. Single-NIC
// config still travels via SANDAL_VM_ARGS and the in-guest container init
// applies it to eth0 (defaulting to DHCP when no addresses are specified).
func RunInVZ(c *config.Config, netFlags []string) error {
	if len(netFlags) > 1 {
		return fmt.Errorf("-vm on darwin supports a single -net flag (VZ attaches one NAT NIC); got %d", len(netFlags))
	}
	if running, _ := crt.IsContainerRunning(c.Name); running {
		return fmt.Errorf("container %s is already running", c.Name)
	}

	// Build args from HostArgs, stripping binary name and "run"
	var rawArgs []string
	if len(c.HostArgs) > 2 {
		rawArgs = c.HostArgs[2:]
	}

	// Remove -vm flag from args -- it's consumed here, not forwarded to VM.
	cleanArgs := RemoveBoolFlag(rawArgs, "vm")

	// If TTY was auto-detected (or explicitly set) on the host but -t
	// isn't in the original args, inject it so the guest container
	// inside the VM allocates a PTY.
	if c.TTY && !HasFlag(cleanArgs, "t") {
		cleanArgs = append([]string{"-t"}, cleanArgs...)
	}

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
	imageDir := filepath.Join(env.LibDir, "image")
	progressCh := make(chan progress.Event, 16)
	renderDone := progress.StartRenderer(progressCh, os.Stderr)
	cleanArgs = squash.PullFromArgs(cleanArgs, imageDir, progressCh)
	close(progressCh)
	<-renderDone
	// Rewrite relative -lw paths to absolute so the in-VM controller can
	// find them via the virtiofs share at /mnt/<abspath>.
	cleanArgs = AbsolutizeLowerPaths(cleanArgs)
	hostPaths = append(hostPaths, ScanLowerPaths(cleanArgs)...)

	// Build VirtioFS mounts from collected host paths.
	mounts, mountEntries, err := BuildVirtioFSMounts(hostPaths, env.LibDir)
	if err != nil {
		return err
	}
	cfg.Mounts = append(cfg.Mounts, mounts...)

	// Share generated /etc files so the VM can access resolv.conf, etc.
	// macOS /etc/resolv.conf is empty; DNS is managed by mDNSResponder.
	// Stage a generated resolv.conf from scutil --dns instead.
	etcDir := stageHostEtc()
	cfg.Mounts = append(cfg.Mounts, vmconfig.MountConfig{
		Tag:      "host-etc",
		HostPath: etcDir,
		ReadOnly: true,
	})
	mountEntries = append(mountEntries, "host-etc="+etcDir+"=/mnt/host-etc")

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

	// Create initrd with sandal binary as /init. Path is owned by the
	// kernel cache (content-addressed) and persists across runs.
	initrdPath, err := PrepareInitrd(cfg.KernelPath, env.VMBinPath)
	if err != nil {
		return err
	}
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

	// Clean up staged etc directory
	os.RemoveAll(filepath.Join(env.LibDir, "system", "etc"))

	// Update status after VM exits
	if err != nil {
		c.Status = fmt.Sprintf("err %v", err)
	} else {
		c.Status = "exit 0"
	}
	controller.SetContainer(c)

	return err
}

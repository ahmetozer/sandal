//go:build darwin || linux

package cmd

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/disk"
	"github.com/ahmetozer/sandal/pkg/vm/kernel"
)

func VM(args []string) error {
	if len(args) < 1 {
		vmUsage()
		return fmt.Errorf("no vm subcommand provided")
	}

	switch args[0] {
	case "create":
		return vmCreate(args[1:])
	case "run":
		return vmRun(args[1:])
	case "start":
		return vmStart(args[1:])
	case "list":
		return vmList()
	case "delete":
		return vmDelete(args[1:])
	case "create-disk":
		return vmCreateDisk(args[1:])
	case "stop":
		return vmStop(args[1:])
	case "kill":
		return vmKill(args[1:])
	default:
		vmUsage()
		return fmt.Errorf("unknown vm command: %s", args[0])
	}
}

func vmUsage() {
	fmt.Fprintln(os.Stderr, `Usage: sandal vm <command> [options]

Commands:
  run          Run an ephemeral VM (created at start, deleted on exit)
  create       Create a new VM configuration
  start        Start a VM (attaches serial console)
  stop         Gracefully stop a running VM
  kill         Force kill a running VM
  list         List all VMs
  delete       Delete one or more VMs (use -all to delete all)
  create-disk  Create a raw disk image`)
}

type repeatableFlag []string

func (r *repeatableFlag) String() string { return strings.Join(*r, ", ") }
func (r *repeatableFlag) Set(val string) error {
	*r = append(*r, val)
	return nil
}

func buildCmdLine(base string, envVars repeatableFlag, initArgs []string) string {
	cmdLine := base
	for _, e := range envVars {
		cmdLine += " " + e
	}
	if len(initArgs) > 0 {
		cmdLine += " init=" + initArgs[0]
		if len(initArgs) > 1 {
			cmdLine += " -- " + strings.Join(initArgs[1:], " ")
		}
	}
	return cmdLine
}

func parseMountFlags(mounts repeatableFlag) ([]vmconfig.MountConfig, error) {
	var mountConfigs []vmconfig.MountConfig
	for _, m := range mounts {
		parts := strings.SplitN(m, ":", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid mount format '%s' (expected tag:path or tag:path:ro)", m)
		}
		mc := vmconfig.MountConfig{Tag: parts[0]}
		mc.HostPath, _ = filepath.Abs(parts[1])
		if len(parts) == 3 && parts[2] == "ro" {
			mc.ReadOnly = true
		}
		mountConfigs = append(mountConfigs, mc)
	}
	return mountConfigs, nil
}

func vmCreate(args []string) error {
	fs := flag.NewFlagSet("vm create", flag.ExitOnError)
	name := fs.String("name", "", "VM name (required)")
	kernelFlag := fs.String("kernel", "", "Path to Linux kernel Image (auto-downloaded if empty)")
	initrdFlag := fs.String("initrd", "", "Path to initrd (optional)")
	cmdLine := fs.String("cmdline", vmconfig.DefaultConsole(), "Kernel command line")
	diskPath := fs.String("disk", "", "Path to disk image (optional)")
	isoPath := fs.String("iso", "", "Path to ISO image (optional, mounted as read-only disk)")
	var mounts repeatableFlag
	fs.Var(&mounts, "mount", "Mount host dir (tag:path or tag:path:ro), repeatable")
	var envVars repeatableFlag
	fs.Var(&envVars, "env", "Environment variable for init (KEY=VALUE), repeatable")
	cpuCount := fs.Uint("cpus", vmconfig.DefaultCPUCount, "Number of CPUs")
	memoryMB := fs.Uint("memory", vmconfig.DefaultMemoryMB, "Memory in MB")
	fs.Parse(args)

	if *name == "" {
		slog.Error("vmCreate", slog.String("error", "-name is required"))
		fs.Usage()
		return fmt.Errorf("-name is required")
	}

	kernelPath := *kernelFlag
	if kernelPath == "" {
		p, err := kernel.EnsureKernel()
		if err != nil {
			return fmt.Errorf("auto-downloading kernel: %w", err)
		}
		kernelPath = p
	}
	kernelAbs, _ := filepath.Abs(kernelPath)
	var diskAbs string
	if *diskPath != "" {
		diskAbs, _ = filepath.Abs(*diskPath)
	}
	initrdPath := *initrdFlag
	if initrdPath == "" {
		p, err := kernel.EnsureInitrd()
		if err != nil {
			return fmt.Errorf("auto-downloading initrd: %w", err)
		}
		initrdPath = p
	}
	initrdAbs, _ := filepath.Abs(initrdPath)
	var isoAbs string
	if *isoPath != "" {
		isoAbs, _ = filepath.Abs(*isoPath)
	}

	mountConfigs, err := parseMountFlags(mounts)
	if err != nil {
		return err
	}

	cfg := vmconfig.VMConfig{
		KernelPath:  kernelAbs,
		InitrdPath:  initrdAbs,
		CommandLine: buildCmdLine(*cmdLine, envVars, fs.Args()),
		DiskPath:    diskAbs,
		ISOPath:     isoAbs,
		Mounts:      mountConfigs,
		CPUCount:    *cpuCount,
		MemoryBytes: uint64(*memoryMB) * vmconfig.MB,
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validation: %w", err)
	}

	if err := vmconfig.SaveConfig(*name, cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	slog.Info("vmCreate", slog.String("name", *name), slog.String("status", "created"))
	return nil
}

func vmRun(args []string) error {
	fs := flag.NewFlagSet("vm run", flag.ExitOnError)
	name := fs.String("name", "", "VM name (auto-generated if empty)")
	kernelFlag := fs.String("kernel", "", "Path to Linux kernel Image (auto-downloaded if empty)")
	initrdFlag := fs.String("initrd", "", "Path to initrd (auto-downloaded if empty)")
	cmdLine := fs.String("cmdline", vmconfig.DefaultConsole(), "Kernel command line")
	diskPath := fs.String("disk", "", "Path to disk image (optional)")
	isoPath := fs.String("iso", "", "Path to ISO image (optional)")
	var mounts repeatableFlag
	fs.Var(&mounts, "mount", "Mount host dir (tag:path or tag:path:ro), repeatable")
	var envVars repeatableFlag
	fs.Var(&envVars, "env", "Environment variable for init (KEY=VALUE), repeatable")
	cpuCount := fs.Uint("cpus", vmconfig.DefaultCPUCount, "Number of CPUs")
	memoryMB := fs.Uint("memory", vmconfig.DefaultMemoryMB, "Memory in MB")
	fs.Parse(args)

	kernelPath := *kernelFlag
	if kernelPath == "" {
		p, err := kernel.EnsureKernel()
		if err != nil {
			return fmt.Errorf("auto-downloading kernel: %w", err)
		}
		kernelPath = p
	}

	vmName := *name
	if vmName == "" {
		vmName = fmt.Sprintf("run-%d", os.Getpid())
	}

	kernelAbs, _ := filepath.Abs(kernelPath)
	var diskAbs string
	if *diskPath != "" {
		diskAbs, _ = filepath.Abs(*diskPath)
	}
	initrdPath := *initrdFlag
	if initrdPath == "" {
		p, err := kernel.EnsureInitrd()
		if err != nil {
			return fmt.Errorf("auto-downloading initrd: %w", err)
		}
		initrdPath = p
	}
	initrdAbs, _ := filepath.Abs(initrdPath)
	var isoAbs string
	if *isoPath != "" {
		isoAbs, _ = filepath.Abs(*isoPath)
	}

	mountConfigs, err := parseMountFlags(mounts)
	if err != nil {
		return err
	}

	cfg := vmconfig.VMConfig{
		KernelPath:  kernelAbs,
		InitrdPath:  initrdAbs,
		CommandLine: buildCmdLine(*cmdLine, envVars, fs.Args()),
		DiskPath:    diskAbs,
		ISOPath:     isoAbs,
		Mounts:      mountConfigs,
		CPUCount:    *cpuCount,
		MemoryBytes: uint64(*memoryMB) * vmconfig.MB,
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validation: %w", err)
	}

	// Save ephemeral config so list/stop/kill work
	if err := vmconfig.SaveConfig(vmName, cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	// Clean up config on exit
	defer vmconfig.DeleteVM(vmName)

	return startVM(vmName, cfg)
}

func vmStart(args []string) error {
	fs := flag.NewFlagSet("vm start", flag.ExitOnError)
	name := fs.String("name", "", "VM name (required)")
	fs.Parse(args)

	if *name == "" {
		slog.Error("vmStart", slog.String("error", "-name is required"))
		fs.Usage()
		return fmt.Errorf("-name is required")
	}

	cfg, err := vmconfig.LoadConfig(*name)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	return startVM(*name, cfg)
}

func startVM(name string, cfg vmconfig.VMConfig) error {
	// Check if VM is already running
	if pid, alive := vmconfig.ReadPid(name); alive {
		return fmt.Errorf("VM '%s' is already running (pid %d)", name, pid)
	}

	// Validate that all referenced files still exist (kernel, initrd, disk, iso, mounts).
	// Config may have been created earlier and files could have been moved or deleted since.
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config validation: %w", err)
	}

	// Auto-generate initramfs overlay for virtiofs mounts (fallback if no cloud-init)
	if len(cfg.Mounts) > 0 && cfg.InitrdPath != "" && cfg.ISOPath == "" {
		var mounts []kernel.MountInfo
		for _, m := range cfg.Mounts {
			mounts = append(mounts, kernel.MountInfo{
				Tag:      m.Tag,
				ReadOnly: m.ReadOnly,
			})
		}
		overlayPath, err := kernel.CreateOverlay(cfg.InitrdPath, mounts)
		if err != nil {
			slog.Warn("startVM", slog.String("action", "initrd overlay"), slog.Any("error", err))
		} else if overlayPath != cfg.InitrdPath {
			defer os.Remove(overlayPath)
			cfg.InitrdPath = overlayPath
		}
	}

	return platformBoot(name, cfg)
}

func vmList() error {
	names, err := vmconfig.ListVMs()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Println("No VMs found.")
		return nil
	}
	for _, name := range names {
		cfg, err := vmconfig.LoadConfig(name)
		if err != nil {
			fmt.Printf("  %s  (error reading config)\n", name)
			continue
		}
		status := "stopped"
		if pid, alive := vmconfig.ReadPid(name); alive {
			status = fmt.Sprintf("running (pid %d)", pid)
		}
		fmt.Printf("  %s  [%s]  cpus=%d  memory=%dMB  kernel=%s\n",
			name, status, cfg.CPUCount, cfg.MemoryBytes/vmconfig.MB, cfg.KernelPath)
	}
	return nil
}

func vmDelete(args []string) error {
	fs := flag.NewFlagSet("vm delete", flag.ExitOnError)
	all := fs.Bool("all", false, "Delete all VMs")
	fs.Parse(args)

	var names []string
	if *all {
		var err error
		names, err = vmconfig.ListVMs()
		if err != nil {
			return err
		}
		if len(names) == 0 {
			fmt.Println("No VMs to delete.")
			return nil
		}
	} else {
		names = fs.Args()
		if len(names) == 0 {
			fmt.Fprintln(os.Stderr, "Usage: sandal vm delete <name>... | -all")
			return fmt.Errorf("at least one VM name is required")
		}
	}

	var errs []error
	for _, name := range names {
		if err := vmconfig.DeleteVM(name); err != nil {
			slog.Error("vmDelete", slog.String("name", name), slog.Any("error", err))
			errs = append(errs, err)
		} else {
			slog.Info("vmDelete", slog.String("name", name), slog.String("status", "deleted"))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to delete %d VM(s)", len(errs))
	}
	return nil
}

func vmStop(args []string) error {
	fs := flag.NewFlagSet("vm stop", flag.ExitOnError)
	name := fs.String("name", "", "VM name (required)")
	fs.Parse(args)

	if *name == "" {
		slog.Error("vmStop", slog.String("error", "-name is required"))
		fs.Usage()
		return fmt.Errorf("-name is required")
	}

	pid, alive := vmconfig.ReadPid(*name)
	if !alive {
		return fmt.Errorf("VM '%s' is not running", *name)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("sending SIGTERM to pid %d: %w", pid, err)
	}
	slog.Info("vmStop", slog.String("name", *name), slog.Int("pid", pid), slog.String("signal", "SIGTERM"))
	return nil
}

func vmKill(args []string) error {
	fs := flag.NewFlagSet("vm kill", flag.ExitOnError)
	name := fs.String("name", "", "VM name")
	all := fs.Bool("all", false, "Kill all running VMs")
	force := fs.Bool("force", false, "Skip SIGTERM, send SIGKILL immediately")
	fs.Parse(args)

	var names []string
	if *all {
		listed, err := vmconfig.ListVMs()
		if err != nil {
			return err
		}
		for _, n := range listed {
			if _, alive := vmconfig.ReadPid(n); alive {
				names = append(names, n)
			}
		}
		if len(names) == 0 {
			fmt.Println("No running VMs to kill.")
			return nil
		}
	} else {
		if *name == "" {
			fmt.Fprintln(os.Stderr, "Usage: sandal vm kill -name <name> | -all")
			return fmt.Errorf("-name or -all is required")
		}
		names = []string{*name}
	}

	var errs []error
	for _, n := range names {
		if err := killOneVM(n, *force); err != nil {
			slog.Error("vmKill", slog.String("name", n), slog.Any("error", err))
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed to kill %d VM(s)", len(errs))
	}
	return nil
}

func killOneVM(name string, force bool) error {
	pid, alive := vmconfig.ReadPid(name)
	if !alive {
		vmconfig.RemovePidFile(name)
		exec.Command("stty", "sane").Run()
		return fmt.Errorf("VM '%s' is not running", name)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	if !force {
		slog.Info("vmKill", slog.String("name", name), slog.Int("pid", pid), slog.String("signal", "SIGTERM"))
		if err := proc.Signal(syscall.SIGTERM); err != nil {
			slog.Error("vmKill", slog.String("action", "SIGTERM"), slog.Any("error", err))
		} else {
			for i := 0; i < 30; i++ {
				if err := proc.Signal(syscall.Signal(0)); err != nil {
					vmconfig.RemovePidFile(name)
					slog.Info("vmKill", slog.String("name", name), slog.String("status", "stopped gracefully"))
					exec.Command("stty", "sane").Run()
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			slog.Warn("vmKill", slog.String("action", "graceful shutdown timeout"))
		}
	}

	if err := proc.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("sending SIGKILL to pid %d: %w", pid, err)
	}
	vmconfig.RemovePidFile(name)
	slog.Info("vmKill", slog.String("name", name), slog.Int("pid", pid), slog.String("signal", "SIGKILL"))
	exec.Command("stty", "sane").Run()
	return nil
}

func vmCreateDisk(args []string) error {
	fs := flag.NewFlagSet("vm create-disk", flag.ExitOnError)
	path := fs.String("path", "", "Output disk image path (required)")
	sizeMB := fs.Int64("size", int64(vmconfig.DefaultDiskSizeMB), "Disk size in MB")
	fs.Parse(args)

	if *path == "" {
		slog.Error("vmCreateDisk", slog.String("error", "-path is required"))
		fs.Usage()
		return fmt.Errorf("-path is required")
	}

	sizeBytes := *sizeMB * int64(vmconfig.MB)
	if err := disk.CreateRawDisk(*path, sizeBytes); err != nil {
		return err
	}
	slog.Info("vmCreateDisk", slog.String("path", *path), slog.Int64("sizeMB", *sizeMB))
	return nil
}

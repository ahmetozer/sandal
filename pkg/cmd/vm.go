//go:build darwin

package cmd

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"github.com/ahmetozer/sandal/pkg/vm/cloudinit"
	"github.com/ahmetozer/sandal/pkg/vm/disk"
	"github.com/ahmetozer/sandal/pkg/vm/initrd"
	"github.com/ahmetozer/sandal/pkg/vm/vz"
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
  delete       Delete a VM
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

func parseMountFlags(mounts repeatableFlag) ([]vz.MountConfig, error) {
	var mountConfigs []vz.MountConfig
	for _, m := range mounts {
		parts := strings.SplitN(m, ":", 3)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid mount format '%s' (expected tag:path or tag:path:ro)", m)
		}
		mc := vz.MountConfig{Tag: parts[0]}
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
	kernel := fs.String("kernel", "", "Path to Linux kernel Image (required)")
	initrdFlag := fs.String("initrd", "", "Path to initrd (optional)")
	cmdLine := fs.String("cmdline", "console=hvc0", "Kernel command line")
	diskPath := fs.String("disk", "", "Path to disk image (optional)")
	isoPath := fs.String("iso", "", "Path to ISO image (optional, mounted as read-only disk)")
	var mounts repeatableFlag
	fs.Var(&mounts, "mount", "Mount host dir (tag:path or tag:path:ro), repeatable")
	var envVars repeatableFlag
	fs.Var(&envVars, "env", "Environment variable for init (KEY=VALUE), repeatable")
	cpuCount := fs.Uint("cpus", vz.DefaultCPUCount, "Number of CPUs")
	memoryMB := fs.Uint("memory", vz.DefaultMemoryMB, "Memory in MB")
	fs.Parse(args)

	if *name == "" || *kernel == "" {
		fmt.Fprintln(os.Stderr, "Error: -name and -kernel are required")
		fs.Usage()
		return fmt.Errorf("-name and -kernel are required")
	}

	kernelAbs, _ := filepath.Abs(*kernel)
	var diskAbs string
	if *diskPath != "" {
		diskAbs, _ = filepath.Abs(*diskPath)
	}
	var initrdAbs string
	if *initrdFlag != "" {
		initrdAbs, _ = filepath.Abs(*initrdFlag)
	}
	var isoAbs string
	if *isoPath != "" {
		isoAbs, _ = filepath.Abs(*isoPath)
	}

	mountConfigs, err := parseMountFlags(mounts)
	if err != nil {
		return err
	}

	cfg := vz.VMConfig{
		KernelPath:  kernelAbs,
		InitrdPath:  initrdAbs,
		CommandLine: buildCmdLine(*cmdLine, envVars, fs.Args()),
		DiskPath:    diskAbs,
		ISOPath:     isoAbs,
		Mounts:      mountConfigs,
		CPUCount:    *cpuCount,
		MemoryBytes: uint64(*memoryMB) * vz.MB,
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validation: %w", err)
	}

	if err := vz.SaveConfig(*name, cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}

	fmt.Printf("VM '%s' created.\n", *name)
	return nil
}

func vmRun(args []string) error {
	fs := flag.NewFlagSet("vm run", flag.ExitOnError)
	name := fs.String("name", "", "VM name (auto-generated if empty)")
	kernel := fs.String("kernel", "", "Path to Linux kernel Image (required)")
	initrdFlag := fs.String("initrd", "", "Path to initrd (optional)")
	cmdLine := fs.String("cmdline", "console=hvc0", "Kernel command line")
	diskPath := fs.String("disk", "", "Path to disk image (optional)")
	isoPath := fs.String("iso", "", "Path to ISO image (optional)")
	var mounts repeatableFlag
	fs.Var(&mounts, "mount", "Mount host dir (tag:path or tag:path:ro), repeatable")
	var envVars repeatableFlag
	fs.Var(&envVars, "env", "Environment variable for init (KEY=VALUE), repeatable")
	cpuCount := fs.Uint("cpus", vz.DefaultCPUCount, "Number of CPUs")
	memoryMB := fs.Uint("memory", vz.DefaultMemoryMB, "Memory in MB")
	fs.Parse(args)

	if *kernel == "" {
		fmt.Fprintln(os.Stderr, "Error: -kernel is required")
		fs.Usage()
		return fmt.Errorf("-kernel is required")
	}

	vmName := *name
	if vmName == "" {
		vmName = fmt.Sprintf("run-%d", os.Getpid())
	}

	kernelAbs, _ := filepath.Abs(*kernel)
	var diskAbs string
	if *diskPath != "" {
		diskAbs, _ = filepath.Abs(*diskPath)
	}
	var initrdAbs string
	if *initrdFlag != "" {
		initrdAbs, _ = filepath.Abs(*initrdFlag)
	}
	var isoAbs string
	if *isoPath != "" {
		isoAbs, _ = filepath.Abs(*isoPath)
	}

	mountConfigs, err := parseMountFlags(mounts)
	if err != nil {
		return err
	}

	cfg := vz.VMConfig{
		KernelPath:  kernelAbs,
		InitrdPath:  initrdAbs,
		CommandLine: buildCmdLine(*cmdLine, envVars, fs.Args()),
		DiskPath:    diskAbs,
		ISOPath:     isoAbs,
		Mounts:      mountConfigs,
		CPUCount:    *cpuCount,
		MemoryBytes: uint64(*memoryMB) * vz.MB,
	}

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("validation: %w", err)
	}

	// Save ephemeral config so list/stop/kill work
	if err := vz.SaveConfig(vmName, cfg); err != nil {
		return fmt.Errorf("saving config: %w", err)
	}
	// Clean up config on exit
	defer vz.DeleteVM(vmName)

	return startVM(vmName, cfg)
}

func vmStart(args []string) error {
	fs := flag.NewFlagSet("vm start", flag.ExitOnError)
	name := fs.String("name", "", "VM name (required)")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: -name is required")
		fs.Usage()
		return fmt.Errorf("-name is required")
	}

	cfg, err := vz.LoadConfig(*name)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	return startVM(*name, cfg)
}

func startVM(name string, cfg vz.VMConfig) error {
	// Check if VM is already running
	if pid, alive := vz.ReadPid(name); alive {
		return fmt.Errorf("VM '%s' is already running (pid %d)", name, pid)
	}

	// Validate that all referenced files still exist (kernel, initrd, disk, iso, mounts).
	// Config may have been created earlier and files could have been moved or deleted since.
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config validation: %w", err)
	}

	// Auto-generate cloud-init ISO for virtiofs mounts
	if len(cfg.Mounts) > 0 {
		var ciMounts []cloudinit.MountInfo
		for _, m := range cfg.Mounts {
			ciMounts = append(ciMounts, cloudinit.MountInfo{
				Tag:      m.Tag,
				ReadOnly: m.ReadOnly,
			})
		}
		isoPath, err := cloudinit.GenerateNoCloudISO(ciMounts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not generate cloud-init ISO: %v\n", err)
		} else if isoPath != "" {
			defer os.Remove(isoPath)
			if cfg.ISOPath == "" {
				cfg.ISOPath = isoPath
			} else {
				fmt.Fprintf(os.Stderr, "Warning: cloud-init ISO generated but -iso already specified, skipping auto-attach\n")
			}
		}
	}

	// Auto-generate initramfs overlay for virtiofs mounts (fallback if no cloud-init)
	if len(cfg.Mounts) > 0 && cfg.InitrdPath != "" && cfg.ISOPath == "" {
		var mounts []initrd.MountInfo
		for _, m := range cfg.Mounts {
			mounts = append(mounts, initrd.MountInfo{
				Tag:      m.Tag,
				ReadOnly: m.ReadOnly,
			})
		}
		overlayPath, err := initrd.CreateOverlay(cfg.InitrdPath, mounts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: could not generate initrd overlay: %v\n", err)
		} else if overlayPath != cfg.InitrdPath {
			defer os.Remove(overlayPath)
			cfg.InitrdPath = overlayPath
		}
	}

	return bootVM(name, cfg)
}

// bootVM boots the VM without applying any cloud-init or initrd overlays.
// Use startVM() for the standard flow with auto-generated overlays.
func bootVM(name string, cfg vz.VMConfig) error {
	// Kill stale VM processes holding the disk file
	if cfg.DiskPath != "" {
		if killed, err := killStaleDiskHolders(cfg.DiskPath); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to check for stale processes: %v\n", err)
		} else if killed > 0 {
			fmt.Fprintf(os.Stderr, "Killed %d stale VM process(es) holding %s\n", killed, cfg.DiskPath)
		}
	}

	// Write PID file
	if err := vz.WritePidFile(name); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not write pid file: %v\n", err)
	}
	defer vz.RemovePidFile(name)

	// Set terminal to raw mode for serial console (skip if not a TTY)
	restore, err := setRawTerminal()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not set raw terminal: %v\n", err)
		restore = func() {} // no-op
	}
	// Ensure terminal is always restored, even on signals
	defer func() {
		restore()
		exec.Command("stty", "sane").Run()
	}()

	vm, err := vz.NewVM(name, cfg)
	if err != nil {
		restore()
		return fmt.Errorf("creating VM: %w", err)
	}

	// Relay serial console I/O between VM and host stdin/stdout
	vm.StartIORelay(os.Stdin, os.Stdout)

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		vm.RequestStop()
		<-sigCh
		vm.Stop()
		vz.StopMainRunLoop()
	}()

	// Start VM asynchronously
	go func() {
		if err := vm.Start(); err != nil {
			restore()
			fmt.Fprintf(os.Stderr, "\nError starting VM: %v\n", err)
			vz.StopMainRunLoop()
			return
		}
		fmt.Fprintf(os.Stderr, "\r\n[VM started - state: %s]\r\n", vm.State())
	}()

	// Wait for VM to stop
	go func() {
		err := vm.WaitUntilStopped()
		if err != nil {
			fmt.Fprintf(os.Stderr, "\r\n[VM stopped with error: %v]\r\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "\r\n[VM stopped]\r\n")
		}
		vz.StopMainRunLoop()
	}()

	// Run the main run loop (blocks until StopMainRunLoop)
	vz.RunMainRunLoop()
	return nil
}

func vmList() error {
	names, err := vz.ListVMs()
	if err != nil {
		return err
	}
	if len(names) == 0 {
		fmt.Println("No VMs found.")
		return nil
	}
	for _, name := range names {
		cfg, err := vz.LoadConfig(name)
		if err != nil {
			fmt.Printf("  %s  (error reading config)\n", name)
			continue
		}
		status := "stopped"
		if pid, alive := vz.ReadPid(name); alive {
			status = fmt.Sprintf("running (pid %d)", pid)
		}
		fmt.Printf("  %s  [%s]  cpus=%d  memory=%dMB  kernel=%s\n",
			name, status, cfg.CPUCount, cfg.MemoryBytes/vz.MB, cfg.KernelPath)
	}
	return nil
}

func vmDelete(args []string) error {
	fs := flag.NewFlagSet("vm delete", flag.ExitOnError)
	name := fs.String("name", "", "VM name (required)")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: -name is required")
		fs.Usage()
		return fmt.Errorf("-name is required")
	}

	if err := vz.DeleteVM(*name); err != nil {
		return err
	}
	fmt.Printf("VM '%s' deleted.\n", *name)
	return nil
}

func vmStop(args []string) error {
	fs := flag.NewFlagSet("vm stop", flag.ExitOnError)
	name := fs.String("name", "", "VM name (required)")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: -name is required")
		fs.Usage()
		return fmt.Errorf("-name is required")
	}

	pid, alive := vz.ReadPid(*name)
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
	fmt.Printf("Sent SIGTERM to VM '%s' (pid %d)\n", *name, pid)
	return nil
}

func vmKill(args []string) error {
	fs := flag.NewFlagSet("vm kill", flag.ExitOnError)
	name := fs.String("name", "", "VM name (required)")
	force := fs.Bool("force", false, "Skip SIGTERM, send SIGKILL immediately")
	fs.Parse(args)

	if *name == "" {
		fmt.Fprintln(os.Stderr, "Error: -name is required")
		fs.Usage()
		return fmt.Errorf("-name is required")
	}

	pid, alive := vz.ReadPid(*name)
	if !alive {
		vz.RemovePidFile(*name)
		exec.Command("stty", "sane").Run()
		return fmt.Errorf("VM '%s' is not running", *name)
	}

	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	if !*force {
		fmt.Printf("Sending SIGTERM to VM '%s' (pid %d)...\n", *name, pid)
		if err := proc.Signal(syscall.SIGTERM); err != nil {
			fmt.Fprintf(os.Stderr, "Error sending SIGTERM: %v\n", err)
		} else {
			for i := 0; i < 30; i++ {
				if err := proc.Signal(syscall.Signal(0)); err != nil {
					vz.RemovePidFile(*name)
					fmt.Printf("VM '%s' stopped gracefully\n", *name)
					exec.Command("stty", "sane").Run()
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			fmt.Println("Graceful shutdown timeout, sending SIGKILL...")
		}
	}

	if err := proc.Signal(syscall.SIGKILL); err != nil {
		return fmt.Errorf("sending SIGKILL to pid %d: %w", pid, err)
	}
	vz.RemovePidFile(*name)
	fmt.Printf("Sent SIGKILL to VM '%s' (pid %d)\n", *name, pid)
	exec.Command("stty", "sane").Run()
	return nil
}

func vmCreateDisk(args []string) error {
	fs := flag.NewFlagSet("vm create-disk", flag.ExitOnError)
	path := fs.String("path", "", "Output disk image path (required)")
	sizeMB := fs.Int64("size", int64(vz.DefaultDiskSizeMB), "Disk size in MB")
	fs.Parse(args)

	if *path == "" {
		fmt.Fprintln(os.Stderr, "Error: -path is required")
		fs.Usage()
		return fmt.Errorf("-path is required")
	}

	sizeBytes := *sizeMB * int64(vz.MB)
	if err := disk.CreateRawDisk(*path, sizeBytes); err != nil {
		return err
	}
	fmt.Printf("Disk image created: %s (%d MB)\n", *path, *sizeMB)
	return nil
}

func killStaleDiskHolders(diskPath string) (int, error) {
	absPath, err := filepath.Abs(diskPath)
	if err != nil {
		absPath = diskPath
	}

	out, err := exec.Command("lsof", "-t", absPath).Output()
	if err != nil {
		return 0, nil
	}

	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return 0, nil
	}

	myPID := os.Getpid()
	killed := 0
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			continue
		}
		if pid == myPID {
			continue
		}
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := proc.Kill(); err == nil {
			proc.Wait()
			killed++
		}
	}
	return killed, nil
}

// Terminal raw mode via ioctl

type termios struct {
	Iflag  uint64
	Oflag  uint64
	Cflag  uint64
	Lflag  uint64
	Cc     [20]uint8
	Ispeed uint64
	Ospeed uint64
}

func setRawTerminal() (restore func(), err error) {
	fd := os.Stdin.Fd()
	var orig termios
	if err := tcgetattr(fd, &orig); err != nil {
		return nil, fmt.Errorf("tcgetattr: %w", err)
	}

	raw := orig
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.ISIG | syscall.IEXTEN
	raw.Iflag &^= syscall.IXON | syscall.ICRNL | syscall.BRKINT | syscall.INPCK | syscall.ISTRIP
	raw.Oflag &^= syscall.OPOST
	raw.Cflag |= syscall.CS8
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	if err := tcsetattr(fd, &raw); err != nil {
		return nil, fmt.Errorf("tcsetattr: %w", err)
	}

	return func() {
		tcsetattr(fd, &orig)
	}, nil
}

func tcgetattr(fd uintptr, t *termios) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TIOCGETA),
		uintptr(unsafe.Pointer(t)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}

func tcsetattr(fd uintptr, t *termios) error {
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TIOCSETA),
		uintptr(unsafe.Pointer(t)),
	)
	if errno != 0 {
		return errno
	}
	return nil
}

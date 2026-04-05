//go:build linux

package sandal

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/ahmetozer/sandal/pkg/container/config"
	"github.com/ahmetozer/sandal/pkg/container/console"
	sandalnet "github.com/ahmetozer/sandal/pkg/container/net"
	"github.com/ahmetozer/sandal/pkg/container/resources"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	squash "github.com/ahmetozer/sandal/pkg/lib/container/image"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kernel"
	"github.com/ahmetozer/sandal/pkg/vm/kvm"
)

// RunInKVM boots a KVM VM with the current sandal binary as /init,
// then re-executes `sandal run` inside the VM with the original args.
func RunInKVM(c *config.Config) error {
	// Build clean args from HostArgs, stripping the binary name and "run" prefix.
	// HostArgs is ["/path/to/sandal", "run", ...flags..., "--", ...cmd...]
	var rawArgs []string
	if len(c.HostArgs) > 2 {
		rawArgs = c.HostArgs[2:]
	}

	// Remove -vm flag from args -- it's consumed here, not forwarded to guest
	cleanArgs := RemoveBoolFlag(rawArgs, "vm")

	// Remove -cpu and -memory from forwarded args -- they apply to the VM, not guest cgroups
	_, cleanArgs = ExtractFlag(cleanArgs, "cpu", "")
	_, cleanArgs = ExtractFlag(cleanArgs, "memory", "")

	// Remove host-only flags from forwarded args. These apply to the host VM process,
	// not the container inside the guest:
	// - -d/-startup: would cause guest to background/delegate, causing immediate exit
	// - --name: would cause guest container to overwrite host's state file (same name)
	cleanArgs = RemoveBoolFlag(cleanArgs, "d")
	cleanArgs = RemoveBoolFlag(cleanArgs, "startup")
	_, cleanArgs = ExtractFlag(cleanArgs, "name", "")

	// Scan args for -v values to determine VirtioFS shares and socket mounts
	hostPaths, socketMounts := ScanMountPaths(cleanArgs)

	// Pre-pull OCI images on the host
	sandalLibDir := env.LibDir
	cleanArgs = squash.PullFromArgs(cleanArgs, env.BaseImageDir)

	// Build VM config: try loading a named config for this container, fall back to defaults
	cfg, err := vmconfig.LoadConfig(c.Name)
	if err != nil {
		cfg = vmconfig.VMConfig{
			CPUCount:    vmconfig.DefaultCPUCount,
			MemoryBytes: vmconfig.DefaultMemoryMB * vmconfig.MB,
		}
	}

	// Use -cpu and -memory values for VM resources
	if c.CPULimit != "" {
		if cpus, err := strconv.ParseFloat(c.CPULimit, 64); err == nil && cpus > 0 {
			cfg.CPUCount = uint(math.Ceil(cpus))
		}
	}
	if c.MemoryLimit != "" {
		if memBytes, err := resources.ParseSize(c.MemoryLimit); err == nil && memBytes > 0 {
			cfg.MemoryBytes = (uint64(memBytes) + 4095) &^ 4095
		}
	}
	// Clear cgroup fields — they were consumed as VM resources
	c.CPULimit = ""
	c.MemoryLimit = ""

	// Auto-download kernel
	kernelPath, err := kernel.EnsureKernel()
	if err != nil {
		return fmt.Errorf("auto-downloading kernel: %w", err)
	}
	cfg.KernelPath = kernelPath

	// Build VirtioFS mounts from -v host paths
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
	for i, sm := range socketMounts {
		port := 5000 + i
		socketEntries = append(socketEntries, fmt.Sprintf("%d=%s=%s", port, sm.HostPath, sm.GuestPath))
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

	// Allocate a network configuration for the VM from the sandal0 bridge
	var vmNetEncoded string
	bridge, bridgeErr := sandalnet.CreateDefaultBridge()
	if bridgeErr != nil && bridgeErr != os.ErrExist {
		slog.Warn("runInKVM", slog.String("action", "create bridge"), slog.Any("error", bridgeErr))
	}
	if bridge != nil {
		hostAddrs, err := sandalnet.GetAddrsByName(sandalnet.DefaultBridgeInterface)
		if err == nil && len(hostAddrs) > 0 {
			conts, _ := controller.Containers()
			link := sandalnet.Link{Name: "eth0"}
			for _, ha := range hostAddrs {
				ip, err := sandalnet.IPRequest(&conts, ha.IPNet)
				if err != nil {
					slog.Warn("runInKVM", slog.String("action", "ip allocation"), slog.Any("error", err))
					continue
				}
				link.Addr = append(link.Addr, sandalnet.Addr{IP: ip, IPNet: ha.IPNet})
				link.Route = append(link.Route, ha)
			}
			if len(link.Addr) > 0 {
				netJSON, err := json.Marshal(link)
				if err == nil {
					vmNetEncoded = base64.StdEncoding.EncodeToString(netJSON)
				}
			}
		}
	}

	// Build kernel command line
	cfg.CommandLine = BuildKernelCmdLine("kvm", argsJSON, mountEntries, vmNetEncoded, socketEntries)

	// Resolve sandal binary for initrd
	selfBin, err := ResolveVMBinary()
	if err != nil {
		return err
	}

	// Create initrd with sandal binary as /init
	initrdPath, err := PrepareInitrd(cfg.KernelPath, selfBin)
	if err != nil {
		return err
	}
	cfg.InitrdPath = initrdPath

	// Save ephemeral VM config (legacy path, kept for sandal vm list compatibility)
	vmName := c.Name
	if err := vmconfig.SaveConfig(vmName, cfg); err != nil {
		return fmt.Errorf("saving ephemeral VM config: %w", err)
	}

	// For startup containers with a running daemon, delegate to daemon.
	// The daemon health check will detect the container has no running PID
	// and call sandal.Run(HostArgs) to boot the VM.
	if c.Background && c.Startup && !env.IsDaemon && controller.GetControllerType() == controller.ControllerTypeServer {
		// Delegation path: daemon will rebuild everything via sandal.Run(),
		// so clean up the ephemeral VM config and initrd now.
		os.Remove(initrdPath)
		vmconfig.DeleteVM(vmName)
		c.HostPid = os.Getpid()
		c.VM = "kvm"
		if err := controller.SetContainer(c); err != nil {
			return fmt.Errorf("registering VM container: %w", err)
		}
		slog.Info("runInKVM", slog.String("action", "delegated to daemon"), slog.String("name", c.Name))
		return nil
	}

	// When running from daemon, fork a child process for the VM so that
	// killing the VM doesn't kill the daemon itself.
	// The forked child needs the VM config and initrd, so cleanup is
	// handled inside forkVMProcess after the child exits.
	if env.IsDaemon || c.Background {
		return forkVMProcess(c, vmName, cfg, socketMounts, initrdPath)
	}

	// Foreground mode: clean up on exit
	defer os.Remove(initrdPath)
	defer vmconfig.DeleteVM(vmName)

	// Foreground mode: register and boot directly in this process
	c.HostPid = os.Getpid()
	c.VM = "kvm"
	c.Status = "running"
	if err := controller.SetContainer(c); err != nil {
		slog.Warn("runInKVM", slog.String("action", "register container"), slog.Any("error", err))
	}
	defer func() {
		if c.Remove {
			controller.DeleteContainer(c.Name)
		}
	}()

	// Start host-side socket relay for vsock
	if len(socketMounts) > 0 {
		go StartHostSocketRelay(socketMounts)
	}

	err = kvm.Boot(vmName, cfg, nil, nil)

	// Update status after VM exits
	if err != nil {
		c.Status = fmt.Sprintf("err %v", err)
	} else {
		c.Status = "exit 0"
	}
	controller.SetContainer(c)

	return err
}

// forkVMProcess starts the VM in a child process so the daemon/CLI isn't blocked.
func forkVMProcess(c *config.Config, vmName string, cfg vmconfig.VMConfig, socketMounts []SocketMount, initrdPath string) error {
	// The child process will boot the VM using "sandal vm start <name>"
	// which loads the saved VM config and calls boot.Boot().
	cmd := exec.Command(env.BinLoc, "vm", "start", "-name", vmName)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	// Set up FIFO console so `sandal attach` can read VM output.
	var consoleCleanup func()
	if err := console.SetupFIFOConsole(c.Name, cmd, &consoleCleanup); err != nil {
		slog.Warn("forkVMProcess", slog.String("action", "fifo console"), slog.Any("error", err))
	}

	if err := cmd.Start(); err != nil {
		if consoleCleanup != nil {
			consoleCleanup()
		}
		return fmt.Errorf("forking VM process: %w", err)
	}

	c.HostPid = cmd.Process.Pid
	c.VM = "kvm"
	c.Status = "running"
	if err := controller.SetContainer(c); err != nil {
		slog.Warn("runInKVM", slog.String("action", "register container"), slog.Any("error", err))
	}

	// Start host-side socket relay for vsock
	if len(socketMounts) > 0 {
		go StartHostSocketRelay(socketMounts)
	}

	// Wait for child to exit and update status
	go func() {
		waitErr := cmd.Wait()
		if consoleCleanup != nil {
			consoleCleanup()
		}
		if waitErr != nil {
			c.Status = fmt.Sprintf("err %v", waitErr)
		} else {
			c.Status = "exit 0"
		}
		controller.SetContainer(c)
		os.Remove(initrdPath)
		vmconfig.DeleteVM(vmName)
		if c.Remove {
			controller.DeleteContainer(c.Name)
		}
	}()

	return nil
}

// StartHostSocketRelay starts a vsock listener for each socket mount.
func StartHostSocketRelay(sockets []SocketMount) {
	for i, sm := range sockets {
		port := uint32(5000 + i)
		go hostRelaySocket(sm.HostPath, port)
	}
}

// hostRelaySocket listens on AF_VSOCK at the given port and for each accepted
// connection, dials the host Unix socket and performs bidirectional relay.
func hostRelaySocket(hostPath string, port uint32) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		slog.Warn("vsock socket failed", slog.Any("err", err))
		return
	}
	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: port}); err != nil {
		unix.Close(fd)
		slog.Warn("vsock bind failed", slog.Uint64("port", uint64(port)), slog.Any("err", err))
		return
	}
	if err := unix.Listen(fd, 8); err != nil {
		unix.Close(fd)
		slog.Warn("vsock listen failed", slog.Uint64("port", uint64(port)), slog.Any("err", err))
		return
	}

	for {
		nfd, _, err := unix.Accept(fd)
		if err != nil {
			slog.Warn("vsock accept failed", slog.Uint64("port", uint64(port)), slog.Any("err", err))
			return
		}
		go func(clientFD int) {
			vsockFile := os.NewFile(uintptr(clientFD), fmt.Sprintf("vsock-client:%d", port))
			defer vsockFile.Close()

			hostConn, err := net.Dial("unix", hostPath)
			if err != nil {
				slog.Warn("host socket dial failed", slog.String("path", hostPath), slog.Any("err", err))
				return
			}
			defer hostConn.Close()

			done := make(chan struct{})
			go func() {
				io.Copy(hostConn, vsockFile)
				done <- struct{}{}
			}()
			io.Copy(vsockFile, hostConn)
			<-done
		}(nfd)
	}
}

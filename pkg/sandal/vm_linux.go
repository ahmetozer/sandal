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
	"strconv"

	"golang.org/x/sys/unix"

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
func RunInKVM(args []string) error {
	// Remove -vm flag from args -- it's consumed here, not forwarded
	_, cleanArgs := ExtractFlag(args, "vm", "")

	// Extract -cpu and -memory flags to apply to the VM itself.
	cpuVal, cleanArgs := ExtractFlag(cleanArgs, "cpu", "")
	memVal, cleanArgs := ExtractFlag(cleanArgs, "memory", "")

	// Scan args for -v values to determine VirtioFS shares and socket mounts
	hostPaths, socketMounts := ScanMountPaths(cleanArgs)

	// Pre-pull OCI images on the host
	sandalLibDir := env.LibDir
	cleanArgs = squash.PullFromArgs(cleanArgs, env.BaseImageDir)

	// Build VM config with defaults
	cfg := vmconfig.VMConfig{
		CPUCount:    vmconfig.DefaultCPUCount,
		MemoryBytes: vmconfig.DefaultMemoryMB * vmconfig.MB,
	}

	if cpuVal != "" {
		if cpus, err := strconv.ParseFloat(cpuVal, 64); err == nil && cpus > 0 {
			cfg.CPUCount = uint(math.Ceil(cpus))
		}
	}
	if memVal != "" {
		if memBytes, err := resources.ParseSize(memVal); err == nil && memBytes > 0 {
			cfg.MemoryBytes = (uint64(memBytes) + 4095) &^ 4095
		}
	}

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
	defer os.Remove(initrdPath)
	cfg.InitrdPath = initrdPath

	// Boot ephemeral VM
	vmName := fmt.Sprintf("run-%d", os.Getpid())
	if err := vmconfig.SaveConfig(vmName, cfg); err != nil {
		return fmt.Errorf("saving ephemeral VM config: %w", err)
	}
	defer vmconfig.DeleteVM(vmName)

	// Start host-side socket relay for vsock
	if len(socketMounts) > 0 {
		go StartHostSocketRelay(socketMounts)
	}

	return kvm.Boot(vmName, cfg)
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

//go:build linux

package run

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
	"strings"

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

// runInKVM boots a KVM VM with the current sandal binary as /init,
// then re-executes `sandal run` inside the VM with the original args.
func runInKVM(args []string) error {
	// Remove -vm flag from args -- it's consumed here, not forwarded
	_, cleanArgs := extractFlag(args, "vm", "")

	// Extract -cpu and -memory flags to apply to the VM itself.
	// These flags are kept in cleanArgs so the container inside the VM
	// can also enforce cgroup limits with the same values.
	cpuVal, cleanArgs := extractFlag(cleanArgs, "cpu", "")
	memVal, cleanArgs := extractFlag(cleanArgs, "memory", "")

	// Scan args for -v values to determine VirtioFS shares and socket mounts
	hostPaths, socketMounts := scanMountPaths(cleanArgs)

	// Pre-pull OCI images on the host and convert to squashfs.
	// Use env.LibDir / env.BaseImageDir so VM and non-VM runs share the same cache.
	sandalLibDir := env.LibDir
	cleanArgs = squash.PullFromArgs(cleanArgs, env.BaseImageDir)

	// Build VM config with defaults
	cfg := vmconfig.VMConfig{
		CPUCount:    vmconfig.DefaultCPUCount,
		MemoryBytes: vmconfig.DefaultMemoryMB * vmconfig.MB,
	}

	// Override VM resources if -cpu or -memory flags were provided
	if cpuVal != "" {
		if cpus, err := strconv.ParseFloat(cpuVal, 64); err == nil && cpus > 0 {
			cfg.CPUCount = uint(math.Ceil(cpus))
		}
	}
	if memVal != "" {
		if memBytes, err := resources.ParseSize(memVal); err == nil && memBytes > 0 {
			// KVM requires page-aligned memory size
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
	mounts, mountEntries, err := buildVirtioFSMounts(hostPaths, sandalLibDir)
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

	// Rewrite socket -v entries so the container inside the VM bind-mounts
	// from the relay socket path under /var/run/sandal/vsock/ instead of
	// the original host path. The relay creates the socket there in VMInit.
	if len(socketMounts) > 0 {
		cleanArgs = rewriteSocketVolumes(cleanArgs, socketMounts)
	}

	// Marshal args for the kernel command line (must be after rewriteSocketVolumes)
	argsJSON, err := marshalVMArgs(cleanArgs)
	if err != nil {
		return err
	}

	// Allocate a network configuration for the VM from the sandal0 bridge.
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
	cfg.CommandLine = buildKernelCmdLine("kvm", argsJSON, mountEntries, vmNetEncoded, socketEntries)

	// Resolve sandal binary for initrd
	selfBin, err := resolveVMBinary()
	if err != nil {
		return err
	}

	// Create initrd with sandal binary as /init
	initrdPath, err := prepareInitrd(cfg.KernelPath, selfBin)
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
		go startHostSocketRelay(socketMounts)
	}

	return kvm.Boot(vmName, cfg)
}

// startHostSocketRelay starts a vsock listener for each socket mount.
// Each listener accepts connections from the guest and relays them to the
// corresponding host Unix socket.
func startHostSocketRelay(sockets []SocketMount) {
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

// rewriteSocketVolumes rewrites -v entries for socket mounts so the source
// path points to the relay socket under /var/run/sandal/vsock/ in the VM.
// The relay in VMInit creates sockets there, and the container's mountVolumes
// bind-mounts them into the container rootfs at the original guest path.
func rewriteSocketVolumes(args []string, sockets []SocketMount) []string {
	socketMap := make(map[string]string) // hostPath -> guestPath
	for _, sm := range sockets {
		socketMap[sm.HostPath] = sm.GuestPath
	}
	var result []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			result = append(result, args[i:]...)
			break
		}
		if args[i] == "-v" && i+1 < len(args) {
			val := args[i+1]
			hostPath := strings.SplitN(val, ":", 2)[0]
			if guestPath, ok := socketMap[hostPath]; ok {
				// Rewrite: source becomes the relay socket path in VM's /var/run/sandal/vsock/
				relayPath := "/var/run/sandal/vsock" + guestPath
				result = append(result, "-v", relayPath+":"+guestPath)
				i++ // skip original value
				continue
			}
		}
		result = append(result, args[i])
	}
	return result
}

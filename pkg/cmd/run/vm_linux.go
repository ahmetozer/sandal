//go:build linux

package run

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"strconv"

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

	// Scan args for -v values to determine VirtioFS shares
	hostPaths := scanMountPaths(cleanArgs)

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

	// Marshal args for the kernel command line
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
	cfg.CommandLine = buildKernelCmdLine("kvm", argsJSON, mountEntries, vmNetEncoded)

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

	return kvm.Boot(vmName, cfg)
}

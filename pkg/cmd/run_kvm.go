//go:build linux

package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	sandalnet "github.com/ahmetozer/sandal/pkg/container/cruntime/net"
	"github.com/ahmetozer/sandal/pkg/controller"
	"github.com/ahmetozer/sandal/pkg/env"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kernel"
)

// runInKVM boots a KVM VM with the current sandal binary as /init,
// then re-executes `sandal run` inside the VM with the original args.
// This mirrors the macOS run_darwin.go flow but uses KVM instead of VZ.
func runInKVM(args []string) error {
	// Remove -vm flag from args — it's consumed here, not forwarded
	_, cleanArgs := extractFlag(args, "vm", "")

	// Scan args for -v values to determine VirtioFS shares
	hostPaths := scanMountPaths(cleanArgs)

	// Build VM config with defaults
	cfg := vmconfig.VMConfig{
		CPUCount:    vmconfig.DefaultCPUCount,
		MemoryBytes: vmconfig.DefaultMemoryMB * vmconfig.MB,
	}

	// Auto-download kernel
	kernelPath, err := kernel.EnsureKernel()
	if err != nil {
		return fmt.Errorf("auto-downloading kernel: %w", err)
	}
	cfg.KernelPath = kernelPath

	// Build VirtioFS mounts from -v host paths
	var mountEntries []string
	seen := make(map[string]bool)

	for i, hostPath := range hostPaths {
		absPath, err := filepath.Abs(hostPath)
		if err != nil {
			return fmt.Errorf("resolving path %q: %w", hostPath, err)
		}

		// VirtioFS only supports directories — use parent dir for files
		shareDir := absPath
		if fi, err := os.Stat(absPath); err == nil && !fi.IsDir() {
			shareDir = filepath.Dir(absPath)
		}

		if seen[shareDir] {
			continue
		}
		seen[shareDir] = true

		tag := fmt.Sprintf("fs-%d", i)
		cfg.Mounts = append(cfg.Mounts, vmconfig.MountConfig{
			Tag:      tag,
			HostPath: shareDir,
		})
		mountEntries = append(mountEntries, fmt.Sprintf("%s=%s", tag, shareDir))
	}

	// Share ~/.sandal/lib/ for OCI image cache
	home, _ := os.UserHomeDir()
	sandalLibDir := filepath.Join(home, ".sandal", "lib")
	os.MkdirAll(sandalLibDir, 0755)
	if !seen[sandalLibDir] {
		seen[sandalLibDir] = true
		tag := "sandal-lib"
		cfg.Mounts = append(cfg.Mounts, vmconfig.MountConfig{
			Tag:      tag,
			HostPath: sandalLibDir,
		})
		mountEntries = append(mountEntries, fmt.Sprintf("%s=%s=%s", tag, sandalLibDir, "/var/lib/sandal"))
	}

	// Pass original args as SANDAL_VM_ARGS via kernel command line.
	// Base64-encode the JSON because kernel cmdline parser treats " as quotes.
	vmArgs := append([]string{"run"}, cleanArgs...)
	argsJSON, err := json.Marshal(vmArgs)
	if err != nil {
		return fmt.Errorf("marshaling VM args: %w", err)
	}
	argsEncoded := base64.StdEncoding.EncodeToString(argsJSON)

	// Allocate a network configuration for the VM from the sandal0 bridge.
	// This mirrors how containers get IPs via Link.defaults() in link.go.
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
	var cmdLineParts []string
	cmdLineParts = append(cmdLineParts, defaultConsole(), "loglevel="+KernelLogLevel)
	cmdLineParts = append(cmdLineParts, "SANDAL_VM=kvm")
	cmdLineParts = append(cmdLineParts, "SANDAL_VM_ARGS="+argsEncoded)
	if vmNetEncoded != "" {
		cmdLineParts = append(cmdLineParts, "SANDAL_VM_NET="+vmNetEncoded)
	}
	if len(mountEntries) > 0 {
		cmdLineParts = append(cmdLineParts, "SANDAL_VM_MOUNTS="+strings.Join(mountEntries, ","))
	}
	// Pass resolved sandal env vars to the VM
	for _, e := range env.GetDefaults() {
		if val := os.Getenv(e.Name); val != "" {
			cmdLineParts = append(cmdLineParts, fmt.Sprintf("%s=%s", e.Name, val))
		}
	}
	cfg.CommandLine = strings.Join(cmdLineParts, " ")

	// Auto-discover base initrd (modules)
	baseInitrd := ""
	kernelDir := filepath.Dir(cfg.KernelPath)
	kernelBase := filepath.Base(cfg.KernelPath)
	if after, ok := strings.CutPrefix(kernelBase, "vmlinuz-"); ok {
		p := filepath.Join(kernelDir, "initramfs-"+after)
		if _, err := os.Stat(p); err == nil {
			baseInitrd = p
		}
	}
	if baseInitrd == "" {
		if p, err := kernel.EnsureInitrd(); err == nil {
			baseInitrd = p
		}
	}

	// Create initrd with sandal binary as /init.
	// If SANDAL_VM_BIN is set, use that (pre-built static binary).
	// Otherwise use the current binary (must be statically linked).
	selfBin := env.VMBinPath
	if _, err := os.Stat(selfBin); err != nil {
		// Fall back to current executable
		selfBin, err = os.Executable()
		if err != nil {
			return fmt.Errorf("resolving self binary: %w", err)
		}
		selfBin, _ = filepath.EvalSymlinks(selfBin)
	}

	initrdPath, err := kernel.CreateFromBinary(selfBin, baseInitrd)
	if err != nil {
		return fmt.Errorf("creating initrd from sandal binary: %w", err)
	}
	defer os.Remove(initrdPath)
	cfg.InitrdPath = initrdPath

	// Boot ephemeral VM
	vmName := fmt.Sprintf("run-%d", os.Getpid())
	if err := vmconfig.SaveConfig(vmName, cfg); err != nil {
		return fmt.Errorf("saving ephemeral VM config: %w", err)
	}
	defer vmconfig.DeleteVM(vmName)

	return bootVM(vmName, cfg)
}

// hasFlag checks if a flag is present in args (handles -flag, -flag=val forms)
func hasFlag(args []string, name string) bool {
	prefix := "-" + name
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == prefix || strings.HasPrefix(arg, prefix+"=") {
			return true
		}
	}
	return false
}

// extractFlag and scanMountPaths are defined in run_darwin.go on macOS.
// On Linux they need to be available too.

func extractFlag(args []string, name string, defaultVal string) (string, []string) {
	val := defaultVal
	prefix := "-" + name
	var clean []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if strings.HasPrefix(arg, prefix+"=") {
			val = arg[len(prefix)+1:]
			continue
		}

		if arg == prefix && i+1 < len(args) {
			val = args[i+1]
			i++
			continue
		}

		clean = append(clean, arg)
	}

	return val, clean
}

func scanMountPaths(args []string) []string {
	var paths []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if args[i] == "-v" && i+1 < len(args) {
			i++
			hostPath := args[i]
			if parts := strings.SplitN(hostPath, ":", 2); len(parts) >= 1 {
				hostPath = parts[0]
			}
			paths = append(paths, hostPath)
		}
	}
	return paths
}

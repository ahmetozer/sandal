//go:build darwin

package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/env"
	"github.com/ahmetozer/sandal/pkg/vm/kernel"
	"github.com/ahmetozer/sandal/pkg/vm/vz"
)

func Run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("no command option provided")
	}

	// Extract -vm flag (darwin-only, not forwarded to VM).
	// If specified, load that config as a base template; otherwise use defaults.
	vmFlag, cleanArgs := extractFlag(args, "vm", "")

	// Scan args for -lw and -v values to determine VirtioFS shares.
	// The args themselves are NOT modified.
	hostPaths := scanMountPaths(cleanArgs)

	// Build VM config: load from template if -vm was specified, otherwise use defaults
	var cfg vz.VMConfig
	if vmFlag != "" {
		var err error
		cfg, err = vz.LoadConfig(vmFlag)
		if err != nil {
			return fmt.Errorf("loading VM config '%s': %w", vmFlag, err)
		}
	} else {
		cfg = vz.VMConfig{
			CPUCount:    vz.DefaultCPUCount,
			MemoryBytes: vz.DefaultMemoryMB * vz.MB,
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

	// Resolve Linux sandal binary (configured via SANDAL_VM_BIN env var)
	if _, err := os.Stat(env.VMBinPath); err != nil {
		return fmt.Errorf("Linux sandal binary not found at %s (cross-compile with: GOOS=linux CGO_ENABLED=0 go build -o %s .)", env.VMBinPath, env.VMBinPath)
	}

	// Build VirtioFS mounts from collected host paths.
	// Each unique host directory gets a VirtioFS share.
	// Mount mapping is passed as SANDAL_VM_MOUNTS=tag=hostpath,tag=hostpath
	var vmMounts []vz.MountConfig
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

		// Deduplicate: skip if this directory is already shared
		if seen[shareDir] {
			continue
		}
		seen[shareDir] = true

		tag := fmt.Sprintf("fs-%d", i)
		vmMounts = append(vmMounts, vz.MountConfig{
			Tag:      tag,
			HostPath: shareDir,
		})
		mountEntries = append(mountEntries, fmt.Sprintf("%s=%s", tag, shareDir))
	}

	cfg.Mounts = append(cfg.Mounts, vmMounts...)

	// Pass ALL original args unchanged as SANDAL_VM_ARGS
	vmArgs := append([]string{"run"}, cleanArgs...)
	argsJSON, err := json.Marshal(vmArgs)
	if err != nil {
		return fmt.Errorf("marshaling VM args: %w", err)
	}

	// Build kernel command line with resolved env vars so the VM
	// inherits the host's sandal configuration.
	var cmdLineParts []string
	cmdLineParts = append(cmdLineParts, "console=hvc0")
	cmdLineParts = append(cmdLineParts, "SANDAL_VM_ARGS="+string(argsJSON))
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

	// Auto-discover initrd alongside the kernel if not configured.
	baseInitrd := cfg.InitrdPath
	if baseInitrd == "" {
		kernelDir := filepath.Dir(cfg.KernelPath)
		candidates := []string{"initramfs-virt", "initramfs-lts", "initrd.img", "initramfs.img"}
		for _, name := range candidates {
			p := filepath.Join(kernelDir, name)
			if _, err := os.Stat(p); err == nil {
				baseInitrd = p
				break
			}
		}
		// Also try versioned initramfs matching the kernel filename pattern
		// (e.g. vmlinuz-virt-6.18.17-r0 -> initramfs-virt-6.18.17-r0)
		if baseInitrd == "" {
			kernelBase := filepath.Base(cfg.KernelPath)
			if after, ok := strings.CutPrefix(kernelBase, "vmlinuz-"); ok {
				p := filepath.Join(kernelDir, "initramfs-"+after)
				if _, err := os.Stat(p); err == nil {
					baseInitrd = p
				}
			}
		}

		// Fallback: auto-download modules initrd if local discovery failed
		if baseInitrd == "" {
			if p, err := kernel.EnsureInitrd(); err == nil {
				baseInitrd = p
			}
		}
	}

	// Create initrd: sandal Linux binary as /init, prepend base initrd if available
	initrdPath, err := kernel.CreateFromBinary(env.VMBinPath, baseInitrd)
	if err != nil {
		return fmt.Errorf("creating initrd from sandal binary: %w", err)
	}
	defer os.Remove(initrdPath)
	cfg.InitrdPath = initrdPath

	// Each run gets an ephemeral VM that is cleaned up on exit.
	vmName := fmt.Sprintf("run-%d", os.Getpid())
	if err := vz.SaveConfig(vmName, cfg); err != nil {
		return fmt.Errorf("saving ephemeral VM config: %w", err)
	}
	defer vz.DeleteVM(vmName)

	return bootVM(vmName, cfg)
}

// extractFlag removes a flag and its value from args, returning the value and cleaned args.
// Handles both "-flag value" and "-flag=value" forms.
func extractFlag(args []string, name string, defaultVal string) (string, []string) {
	val := defaultVal
	prefix := "-" + name
	var clean []string

	for i := 0; i < len(args); i++ {
		arg := args[i]

		// -flag=value form
		if strings.HasPrefix(arg, prefix+"=") {
			val = arg[len(prefix)+1:]
			continue
		}

		// -flag value form
		if arg == prefix && i+1 < len(args) {
			val = args[i+1]
			i++ // skip value
			continue
		}

		clean = append(clean, arg)
	}

	return val, clean
}

// scanMountPaths scans args for -lw and -v flag values and returns the host paths
// that need VirtioFS shares. Does not modify args.
func scanMountPaths(args []string) []string {
	var paths []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if (args[i] == "-lw" || args[i] == "-v") && i+1 < len(args) {
			i++
			hostPath := args[i]
			// For -v, extract host path from host:container format
			if parts := strings.SplitN(hostPath, ":", 2); len(parts) >= 1 {
				hostPath = parts[0]
			}
			paths = append(paths, hostPath)
		}
	}
	return paths
}

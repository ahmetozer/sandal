package run

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ahmetozer/sandal/pkg/env"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kernel"
)

// kernelLogLevel maps the current slog level to a Linux kernel loglevel string.
// debug -> 7, info -> 4, warn -> 2, error/default -> 0
func kernelLogLevel() string {
	switch {
	case slog.Default().Enabled(context.Background(), slog.LevelDebug):
		return "7"
	case slog.Default().Enabled(context.Background(), slog.LevelInfo):
		return "4"
	case slog.Default().Enabled(context.Background(), slog.LevelWarn):
		return "2"
	default:
		return "0"
	}
}

// buildVirtioFSMounts creates VirtioFS mount configs from host paths.
// It deduplicates directories, assigns tags (fs-0, fs-1...), and adds
// a sandal-lib share (tag "sandal-lib", mounted at /var/lib/sandal).
// Returns mount configs and mount entry strings for the kernel command line.
func buildVirtioFSMounts(hostPaths []string, sandalLibDir string) ([]vmconfig.MountConfig, []string, error) {
	var mounts []vmconfig.MountConfig
	var mountEntries []string
	seen := make(map[string]bool)

	for i, hostPath := range hostPaths {
		absPath, err := filepath.Abs(hostPath)
		if err != nil {
			return nil, nil, fmt.Errorf("resolving path %q: %w", hostPath, err)
		}

		// VirtioFS only supports directories -- use parent dir for files
		shareDir := absPath
		if fi, err := os.Stat(absPath); err == nil && !fi.IsDir() {
			shareDir = filepath.Dir(absPath)
		}

		if seen[shareDir] {
			continue
		}
		seen[shareDir] = true

		tag := fmt.Sprintf("fs-%d", i)
		mounts = append(mounts, vmconfig.MountConfig{
			Tag:      tag,
			HostPath: shareDir,
		})
		mountEntries = append(mountEntries, fmt.Sprintf("%s=%s", tag, shareDir))
	}

	// Always share sandalLibDir so OCI images are cached on the host.
	// Inside the VM this is mounted at /var/lib/sandal.
	os.MkdirAll(sandalLibDir, 0755)
	if !seen[sandalLibDir] {
		seen[sandalLibDir] = true
		tag := "sandal-lib"
		mounts = append(mounts, vmconfig.MountConfig{
			Tag:      tag,
			HostPath: sandalLibDir,
		})
		mountEntries = append(mountEntries, fmt.Sprintf("%s=%s=%s", tag, sandalLibDir, "/var/lib/sandal"))
	}

	return mounts, mountEntries, nil
}

// buildKernelCmdLine constructs the kernel command line for a sandal VM.
// vmType is "kvm" or "mac". argsJSON is the JSON-encoded SANDAL_VM_ARGS.
// mountEntries are the SANDAL_VM_MOUNTS entries. netEncoded is base64-encoded
// network config (may be empty).
func buildKernelCmdLine(vmType string, argsJSON []byte, mountEntries []string, netEncoded string) string {
	argsEncoded := base64.StdEncoding.EncodeToString(argsJSON)

	var cmdLineParts []string
	cmdLineParts = append(cmdLineParts, vmconfig.DefaultConsole(), "loglevel="+kernelLogLevel())
	cmdLineParts = append(cmdLineParts, "SANDAL_VM="+vmType)
	cmdLineParts = append(cmdLineParts, "SANDAL_VM_ARGS="+argsEncoded)
	if netEncoded != "" {
		cmdLineParts = append(cmdLineParts, "SANDAL_VM_NET="+netEncoded)
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
	return strings.Join(cmdLineParts, " ")
}

// prepareInitrd discovers an initrd alongside the kernel and creates an
// initrd containing the sandal binary as /init.
// kernelPath is the path to the kernel image. binPath is the sandal binary
// to embed as /init.
func prepareInitrd(kernelPath string, binPath string) (string, error) {
	baseInitrd := ""
	kernelDir := filepath.Dir(kernelPath)

	// Try well-known initrd names
	candidates := []string{"initramfs-virt", "initramfs-lts", "initrd.img", "initramfs.img"}
	for _, name := range candidates {
		p := filepath.Join(kernelDir, name)
		if _, err := os.Stat(p); err == nil {
			baseInitrd = p
			break
		}
	}

	// Try versioned initramfs matching the kernel filename pattern
	// (e.g. vmlinuz-virt-6.18.17-r0 -> initramfs-virt-6.18.17-r0)
	if baseInitrd == "" {
		kernelBase := filepath.Base(kernelPath)
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

	initrdPath, err := kernel.CreateFromBinary(binPath, baseInitrd)
	if err != nil {
		return "", fmt.Errorf("creating initrd from sandal binary: %w", err)
	}
	return initrdPath, nil
}

// resolveVMBinary returns the path to the sandal binary that will be
// embedded in the VM initrd. It checks env.VMBinPath first, then falls
// back to the current executable.
func resolveVMBinary() (string, error) {
	selfBin := env.VMBinPath
	if _, err := os.Stat(selfBin); err != nil {
		// Fall back to current executable
		var exeErr error
		selfBin, exeErr = os.Executable()
		if exeErr != nil {
			return "", fmt.Errorf("resolving self binary: %w", exeErr)
		}
		selfBin, _ = filepath.EvalSymlinks(selfBin)
	}
	return selfBin, nil
}

// marshalVMArgs encodes the cleaned args into a JSON byte slice
// suitable for passing as SANDAL_VM_ARGS.
func marshalVMArgs(cleanArgs []string) ([]byte, error) {
	vmArgs := append([]string{"run"}, cleanArgs...)
	argsJSON, err := json.Marshal(vmArgs)
	if err != nil {
		return nil, fmt.Errorf("marshaling VM args: %w", err)
	}
	return argsJSON, nil
}

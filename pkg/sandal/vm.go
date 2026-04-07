package sandal

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
	squash "github.com/ahmetozer/sandal/pkg/lib/container/image"
	vmconfig "github.com/ahmetozer/sandal/pkg/vm/config"
	"github.com/ahmetozer/sandal/pkg/vm/kernel"
)

// SocketMount represents a Unix socket to relay between host and guest via vsock.
type SocketMount struct {
	HostPath  string
	GuestPath string
}

// kernelLogLevel maps the current slog level to a Linux kernel loglevel string.
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

// BuildVirtioFSMounts creates VirtioFS mount configs from host paths.
// It deduplicates directories, assigns tags (fs-0, fs-1...), and adds
// a sandal-lib share (tag "sandal-lib", mounted at /var/lib/sandal).
func BuildVirtioFSMounts(hostPaths []string, sandalLibDir string) ([]vmconfig.MountConfig, []string, error) {
	var mounts []vmconfig.MountConfig
	var mountEntries []string
	seen := make(map[string]bool)

	for i, hostPath := range hostPaths {
		absPath, err := filepath.Abs(hostPath)
		if err != nil {
			return nil, nil, fmt.Errorf("resolving path %q: %w", hostPath, err)
		}

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

// BuildKernelCmdLine constructs the kernel command line for a sandal VM.
func BuildKernelCmdLine(vmType string, argsJSON []byte, mountEntries []string, netEncoded string, socketEntries []string) string {
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
	if len(socketEntries) > 0 {
		cmdLineParts = append(cmdLineParts, "SANDAL_VM_SOCKETS="+strings.Join(socketEntries, ","))
	}
	for _, e := range env.GetDefaults() {
		if val := os.Getenv(e.Name); val != "" {
			cmdLineParts = append(cmdLineParts, fmt.Sprintf("%s=%s", e.Name, val))
		}
	}
	return strings.Join(cmdLineParts, " ")
}

// PrepareInitrd discovers an initrd alongside the kernel and creates an
// initrd containing the sandal binary as /init.
func PrepareInitrd(kernelPath string, binPath string) (string, error) {
	baseInitrd := ""
	kernelDir := filepath.Dir(kernelPath)

	candidates := []string{"initramfs-virt", "initramfs-lts", "initrd.img", "initramfs.img"}
	for _, name := range candidates {
		p := filepath.Join(kernelDir, name)
		if _, err := os.Stat(p); err == nil {
			baseInitrd = p
			break
		}
	}

	if baseInitrd == "" {
		kernelBase := filepath.Base(kernelPath)
		if after, ok := strings.CutPrefix(kernelBase, "vmlinuz-"); ok {
			p := filepath.Join(kernelDir, "initramfs-"+after)
			if _, err := os.Stat(p); err == nil {
				baseInitrd = p
			}
		}
	}

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

// ResolveVMBinary returns the path to the sandal binary that will be
// embedded in the VM initrd.
func ResolveVMBinary() (string, error) {
	selfBin := env.VMBinPath
	if _, err := os.Stat(selfBin); err != nil {
		var exeErr error
		selfBin, exeErr = os.Executable()
		if exeErr != nil {
			return "", fmt.Errorf("resolving self binary: %w", exeErr)
		}
		selfBin, _ = filepath.EvalSymlinks(selfBin)
	}
	return selfBin, nil
}

// MarshalVMArgs encodes the cleaned args into a JSON byte slice
// suitable for passing as SANDAL_VM_ARGS.
func MarshalVMArgs(cleanArgs []string) ([]byte, error) {
	vmArgs := append([]string{"run"}, cleanArgs...)
	argsJSON, err := json.Marshal(vmArgs)
	if err != nil {
		return nil, fmt.Errorf("marshaling VM args: %w", err)
	}
	return argsJSON, nil
}

// RewriteSocketVolumes rewrites -v entries for socket mounts so the source
// path points to the relay socket under /var/run/sandal/vsock/ in the VM.
func RewriteSocketVolumes(args []string, sockets []SocketMount) []string {
	socketMap := make(map[string]string)
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
				relayPath := "/var/run/sandal/vsock" + guestPath
				result = append(result, "-v", relayPath+":"+guestPath)
				i++
				continue
			}
		}
		result = append(result, args[i])
	}
	return result
}

// ScanLowerPaths scans args for -lw flag values and returns the host paths
// that need to be shared into the VM via VirtioFS so that the in-VM
// mountRootfs() can locate the same source file or directory.
//
// Image references (e.g. "alpine:latest") are skipped: PullFromArgs has
// already rewritten them to a path under env.BaseImageDir, which is shared
// via the sandal-lib mount.
func ScanLowerPaths(args []string) []string {
	var paths []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if args[i] == "-lw" && i+1 < len(args) {
			i++
			val := args[i]

			// Strip :=sub suffix.
			val = strings.TrimSuffix(val, ":=sub")

			// Strip optional ":/target" suffix using last ":/" (matches parseLowerArg).
			source := val
			if idx := strings.LastIndex(val, ":/"); idx > 0 {
				source = val[:idx]
			}

			// Strip disk options like ":part=2".
			basePath := source
			if p := strings.SplitN(source, ":", 2); len(p) > 0 {
				basePath = p[0]
			}

			// Skip image references — handled by squash.PullFromArgs.
			if squash.IsImageReference(source) {
				continue
			}

			// Only share things that actually exist on the host. Anything
			// that doesn't stat will either be an image ref (already
			// handled) or a user typo we want to surface inside the VM.
			if _, err := os.Stat(basePath); err != nil {
				continue
			}

			paths = append(paths, basePath)
		}
	}
	return paths
}

// ScanMountPaths scans args for -v flag values and returns the host paths
// that need VirtioFS shares and socket mounts that need vsock relay.
func ScanMountPaths(args []string) ([]string, []SocketMount) {
	var paths []string
	var sockets []SocketMount
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			break
		}
		if args[i] == "-v" && i+1 < len(args) {
			i++
			val := args[i]

			parts := strings.SplitN(val, ":", 3)
			hostPath := parts[0]
			guestPath := hostPath
			opts := ""
			if len(parts) >= 2 {
				guestPath = parts[1]
			}
			if len(parts) >= 3 {
				opts = parts[2]
			}

			if strings.Contains(opts, "sock") {
				sockets = append(sockets, SocketMount{HostPath: hostPath, GuestPath: guestPath})
				continue
			}

			if fi, err := os.Stat(hostPath); err == nil && fi.Mode().Type()&os.ModeSocket != 0 {
				sockets = append(sockets, SocketMount{HostPath: hostPath, GuestPath: guestPath})
				continue
			}

			paths = append(paths, hostPath)
		}
	}
	return paths, sockets
}

package kernel

import (
	"archive/tar"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ahmetozer/sandal/pkg/lib/apk"
)

const (
	pkgName    = "linux-virt"
	modulesDir = "lib/modules/"
)

func alpineArch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "aarch64"
	case "amd64":
		return "x86_64"
	default:
		return runtime.GOARCH
	}
}

func apkBaseURL() string {
	return "https://dl-cdn.alpinelinux.org/alpine/edge/main/" + alpineArch()
}

func kernelEntry() string {
	return "boot/vmlinuz-virt"
}

var cachedVersion string

func cacheDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".sandal-vm", "kernel")
}

// EnsureKernel returns the path to a cached kernel image, downloading it if necessary.
// Also extracts kernel modules from the same APK for use by EnsureInitrd.
func EnsureKernel() (string, error) {
	dir := cacheDir()

	// Check if any cached kernel+initramfs pair exists before hitting the network.
	if path, err := findCachedKernel(dir); err == nil {
		return path, nil
	}

	version, err := latestVersion()
	if err != nil {
		return "", fmt.Errorf("fetching latest kernel version: %w", err)
	}

	kernelPath := filepath.Join(dir, fmt.Sprintf("vmlinuz-virt-%s", version))
	initrdPath := filepath.Join(dir, fmt.Sprintf("initramfs-virt-%s", version))

	// Check again with the resolved version (in case cache dir scan missed it)
	if _, err := os.Stat(kernelPath); err == nil {
		if _, err := os.Stat(initrdPath); err == nil {
			return kernelPath, nil
		}
	}

	fmt.Fprintf(os.Stderr, "Downloading kernel %s-%s ...\n", pkgName, version)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating cache dir: %w", err)
	}

	apkURL := fmt.Sprintf("%s/%s-%s.apk", apkBaseURL(), pkgName, version)
	if err := downloadAndExtractAPK(apkURL, dir, version); err != nil {
		os.Remove(kernelPath)
		return "", fmt.Errorf("downloading kernel: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Kernel cached at %s\n", kernelPath)
	return kernelPath, nil
}

// EnsureInitrd returns the path to a cached initramfs containing kernel modules.
// The modules are extracted from the same APK as the kernel, ensuring version match.
func EnsureInitrd() (string, error) {
	dir := cacheDir()

	// Return cached initramfs without hitting the network if a kernel+initrd pair exists.
	if kernelPath, err := findCachedKernel(dir); err == nil {
		version := strings.TrimPrefix(filepath.Base(kernelPath), "vmlinuz-virt-")
		return filepath.Join(dir, "initramfs-virt-"+version), nil
	}

	// No cache — fall back to downloading.
	if _, err := EnsureKernel(); err != nil {
		return "", err
	}

	// EnsureKernel creates the initrd as a side effect; find it now.
	if kernelPath, err := findCachedKernel(dir); err == nil {
		version := strings.TrimPrefix(filepath.Base(kernelPath), "vmlinuz-virt-")
		return filepath.Join(dir, "initramfs-virt-"+version), nil
	}

	return "", fmt.Errorf("initramfs not found after kernel download")
}

// downloadAndExtractAPK downloads the kernel APK and extracts:
// 1. The kernel image (decompressed from ZBOOT if needed)
// 2. A modules initramfs (cpio.gz of lib/modules/)
func downloadAndExtractAPK(url, cacheDir, version string) error {
	kernelPath := filepath.Join(cacheDir, fmt.Sprintf("vmlinuz-virt-%s", version))
	initrdPath := filepath.Join(cacheDir, fmt.Sprintf("initramfs-virt-%s", version))

	var modules []moduleFile
	foundKernel := false

	err := apk.Download(url, func(e apk.Entry) error {
		// Extract kernel
		if e.Name == kernelEntry() {
			raw, err := decompressZBoot(e.Data)
			if err != nil {
				return fmt.Errorf("decompressing kernel: %w", err)
			}
			if err := os.WriteFile(kernelPath, raw, 0644); err != nil {
				return err
			}
			foundKernel = true
		}

		// Collect module files (lib/modules/...)
		if strings.HasPrefix(e.Name, modulesDir) && e.Typeflag == tar.TypeReg {
			modules = append(modules, moduleFile{
				name: e.Name,
				data: e.Data,
				mode: e.Mode,
			})
		}

		return nil
	})
	if err != nil {
		return err
	}

	if !foundKernel {
		return fmt.Errorf("kernel entry %s not found in APK", kernelEntry())
	}

	// Build initramfs from collected modules
	if len(modules) > 0 {
		fmt.Fprintf(os.Stderr, "Building modules initramfs (%d modules) ...\n", len(modules))
		if err := buildModulesInitrd(initrdPath, modules); err != nil {
			os.Remove(initrdPath)
			return fmt.Errorf("building modules initrd: %w", err)
		}
	}

	return nil
}

// findCachedKernel looks for an existing vmlinuz-virt-* + initramfs-virt-* pair
// in dir and returns the kernel path if found. This avoids a network round-trip
// to resolve the latest version when a cached kernel already exists.
func findCachedKernel(dir string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "vmlinuz-virt-*"))
	if err != nil || len(matches) == 0 {
		return "", fmt.Errorf("no cached kernel found")
	}
	// Use the most recent match (last in sorted glob output).
	kernelPath := matches[len(matches)-1]
	version := strings.TrimPrefix(filepath.Base(kernelPath), "vmlinuz-virt-")
	initrdPath := filepath.Join(dir, fmt.Sprintf("initramfs-virt-%s", version))
	if _, err := os.Stat(initrdPath); err != nil {
		return "", fmt.Errorf("initramfs not cached for %s", version)
	}
	return kernelPath, nil
}

// latestVersion fetches APKINDEX and returns the version string for linux-virt.
// The result is cached for the lifetime of the process.
func latestVersion() (string, error) {
	if cachedVersion != "" {
		return cachedVersion, nil
	}

	v, err := apk.LatestVersion(apkBaseURL(), pkgName)
	if err != nil {
		return "", err
	}
	cachedVersion = v
	return v, nil
}

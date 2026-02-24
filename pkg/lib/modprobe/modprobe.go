//go:build linux

package modprobe

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Load loads a kernel module by name, resolving and loading its dependencies first.
// Modules that are already loaded are skipped.
// If modules.dep is not available (e.g. minimal initramfs), falls back to
// searching for .ko files directly (without dependency resolution).
func Load(name string) error {
	release, err := kernelRelease()
	if err != nil {
		return fmt.Errorf("getting kernel release: %w", err)
	}
	baseDir := filepath.Join("/lib/modules", release)

	loaded, err := loadedModules()
	if err != nil {
		// Non-fatal: we can still try to load and let EEXIST handle duplicates
		loaded = make(map[string]bool)
	}

	normalized := strings.ReplaceAll(name, "-", "_")

	// If already loaded, nothing to do
	if loaded[normalized] {
		return nil
	}

	// Try modules.dep first for proper dependency resolution
	deps, depErr := parseDeps(baseDir)
	if depErr == nil {
		if entry, ok := deps[normalized]; ok {
			// Load dependencies first (in order)
			for _, depPath := range entry.deps {
				depName := normalizeModName(depPath)
				if loaded[depName] {
					continue
				}
				fullPath := filepath.Join(baseDir, depPath)
				if err := loadModuleFile(fullPath, ""); err != nil {
					return fmt.Errorf("loading dependency %s: %w", depName, err)
				}
				loaded[depName] = true
			}
			// Load the target module
			return loadModuleFile(filepath.Join(baseDir, entry.filePath), "")
		}
	}

	// Fallback: search for the .ko file directly (no dependency resolution).
	// This handles minimal initramfs environments without modules.dep.
	modFile, err := findModuleFile(baseDir, normalized)
	if err != nil {
		return fmt.Errorf("module %q not found in %s: %w", name, baseDir, err)
	}
	return loadModuleFile(modFile, "")
}

// findModuleFile walks baseDir looking for a module file matching the given
// normalized name. Returns the full path to the first match.
func findModuleFile(baseDir string, normalized string) (string, error) {
	var found string
	err := filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return err
		}
		if normalizeModName(path) == normalized {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("no .ko file found for %q", normalized)
	}
	return found, nil
}

// kernelRelease returns the kernel version string (equivalent to `uname -r`).
func kernelRelease() (string, error) {
	var uname unix.Utsname
	if err := unix.Uname(&uname); err != nil {
		return "", err
	}
	// Convert [65]byte to string, trimming at null terminator
	release := unix.ByteSliceToString(uname.Release[:])
	return release, nil
}

// loadedModules parses /proc/modules and returns a set of normalized module names.
func loadedModules() (map[string]bool, error) {
	f, err := os.Open("/proc/modules")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	loaded := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) > 0 {
			name := strings.ReplaceAll(fields[0], "-", "_")
			loaded[name] = true
		}
	}
	return loaded, scanner.Err()
}

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
		return loadWithDeps(normalized, baseDir, deps, loaded)
	}

	// Fallback: search for the .ko file directly (no dependency resolution).
	// This handles minimal initramfs environments without modules.dep.
	modFile, err := findModuleFile(baseDir, normalized)
	if err != nil {
		return fmt.Errorf("module %q not found in %s: %w", name, baseDir, err)
	}
	return loadModuleFile(modFile, "")
}

// loadWithDeps recursively loads a module and its dependencies using modules.dep.
// Each dependency's own dependencies are resolved before loading it, ensuring
// the correct load order regardless of how modules.dep lists them.
func loadWithDeps(name, baseDir string, deps map[string]depEntry, loaded map[string]bool) error {
	if loaded[name] {
		return nil
	}

	entry, ok := deps[name]
	if !ok {
		// Module not in modules.dep — try to find and load directly
		modFile, err := findModuleFile(baseDir, name)
		if err != nil {
			return fmt.Errorf("module %q not found in %s: %w", name, baseDir, err)
		}
		err = loadModuleFile(modFile, "")
		if err == nil {
			loaded[name] = true
		}
		return err
	}

	// Recursively load dependencies first
	for _, depPath := range entry.deps {
		depName := normalizeModName(depPath)
		if loaded[depName] {
			continue
		}
		if err := loadWithDeps(depName, baseDir, deps, loaded); err != nil {
			// Non-fatal: dependency may be missing or be a soft dep.
			// The kernel will reject the target module if required
			// symbols are truly unavailable.
			fmt.Fprintf(os.Stderr, "modprobe: optional dependency %s: %v\n", depName, err)
		}
	}

	// Load the target module
	fullPath := filepath.Join(baseDir, entry.filePath)
	if err := loadModuleFile(fullPath, ""); err != nil {
		return err
	}
	loaded[name] = true
	return nil
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

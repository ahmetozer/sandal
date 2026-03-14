//go:build linux

package modprobe

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type depEntry struct {
	filePath string   // relative path of the module (e.g. "kernel/fs/fuse/fuse.ko.gz")
	deps     []string // relative paths of dependencies, in load order
}

// parseDeps reads and parses a modules.dep file from the given base directory.
// Returns a map from normalized module name to depEntry.
func parseDeps(baseDir string) (map[string]depEntry, error) {
	f, err := os.Open(filepath.Join(baseDir, "modules.dep"))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	deps := make(map[string]depEntry)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || line[0] == '#' {
			continue
		}

		colonIdx := strings.IndexByte(line, ':')
		if colonIdx < 0 {
			continue
		}

		modPath := strings.TrimSpace(line[:colonIdx])
		depsPart := strings.TrimSpace(line[colonIdx+1:])

		name := normalizeModName(modPath)
		entry := depEntry{filePath: modPath}

		if depsPart != "" {
			entry.deps = strings.Fields(depsPart)
		}

		deps[name] = entry
	}

	return deps, scanner.Err()
}

// normalizeModName extracts a normalized module name from a file path.
// e.g. "kernel/fs/fuse/virtiofs.ko.gz" -> "virtiofs"
// Dashes are replaced with underscores for consistent lookup.
func normalizeModName(filePath string) string {
	base := path.Base(filePath)
	// Strip all known extensions: .ko, .ko.gz, .ko.xz, .ko.zst
	for _, ext := range []string{".ko.gz", ".ko.xz", ".ko.zst", ".ko"} {
		if strings.HasSuffix(base, ext) {
			base = base[:len(base)-len(ext)]
			break
		}
	}
	return strings.ReplaceAll(base, "-", "_")
}

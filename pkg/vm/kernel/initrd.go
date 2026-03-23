package kernel

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type moduleFile struct {
	name string
	data []byte
	mode int64
}

// MountInfo describes a virtiofs mount to inject into the init script.
type MountInfo struct {
	Tag      string
	ReadOnly bool
}

// buildModulesInitrd creates a gzip-compressed CPIO archive containing
// the kernel modules tree (lib/modules/...).
func buildModulesInitrd(destPath string, modules []moduleFile) error {
	var cpio bytes.Buffer

	// Collect unique directories
	dirs := make(map[string]bool)
	for _, m := range modules {
		dir := filepath.Dir(m.name)
		for dir != "." && dir != "" {
			dirs[dir] = true
			dir = filepath.Dir(dir)
		}
	}

	// Write directory entries sorted so parents come before children
	// (the kernel's CPIO extractor uses non-recursive mkdir)
	sortedDirs := make([]string, 0, len(dirs))
	for dir := range dirs {
		sortedDirs = append(sortedDirs, dir)
	}
	sort.Strings(sortedDirs)

	inode := uint32(1)
	for _, dir := range sortedDirs {
		writeCPIODir(&cpio, dir, inode)
		inode++
	}

	// Write file entries
	for _, m := range modules {
		mode := uint32(m.mode) | 0100000 // Ensure S_IFREG file type bit is set
		if mode == 0100000 {
			mode = 0100644
		}
		writeCPIOFile(&cpio, m.name, m.data, mode, inode)
		inode++
	}

	// Trailer
	writeCPIOFile(&cpio, "TRAILER!!!", nil, 0, 0)

	// Gzip compress
	f, err := os.Create(destPath)
	if err != nil {
		return err
	}
	gz := gzip.NewWriter(f)
	if _, err := gz.Write(cpio.Bytes()); err != nil {
		f.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// CreateOverlay generates a modified initramfs with virtiofs mount commands
// injected before each exec switch_root call. It appends a CPIO overlay to
// the original initramfs. The kernel processes concatenated archives in order,
// so the modified /init overrides the original.
// Returns the path to a temporary file (caller must remove it).
func CreateOverlay(initrdPath string, mounts []MountInfo) (string, error) {
	if len(mounts) == 0 {
		return initrdPath, nil
	}

	initScript, err := extractInit(initrdPath)
	if err != nil {
		return "", fmt.Errorf("extract init from initramfs: %w", err)
	}

	modified := injectMounts(string(initScript), mounts)
	overlay := newcCPIO("init", []byte(modified), 0100755)

	origData, err := os.ReadFile(initrdPath)
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp("", "sandal-initrd-*.img")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	if _, err := tmp.Write(origData); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if _, err := tmp.Write(overlay); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}

	return tmp.Name(), nil
}

// CreateFromBinary creates an initramfs CPIO archive with the given binary as /init.
// If baseInitrdPath is non-empty, the base initrd's CPIO entries are merged into
// a single CPIO archive so kernel modules remain available. The binary is added
// as /init after the base entries.
//
// The result is a single gzip-compressed CPIO archive (not concatenated archives)
// because the kernel's initramfs unpacker stops processing at the first TRAILER
// entry within a CPIO stream.
//
// Returns the path to a temporary file (caller must remove it).
func CreateFromBinary(binaryPath string, baseInitrdPath string) (string, error) {
	binData, err := os.ReadFile(binaryPath)
	if err != nil {
		return "", fmt.Errorf("reading binary %s: %w", binaryPath, err)
	}

	tmp, err := os.CreateTemp("", "sandal-initrd-*.img")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	gz := gzip.NewWriter(tmp)

	// Include base initrd content (decompressed CPIO, trailer stripped)
	// for kernel modules.
	if baseInitrdPath != "" {
		baseData, err := os.ReadFile(baseInitrdPath)
		if err != nil {
			os.Remove(tmp.Name())
			return "", fmt.Errorf("reading base initrd %s: %w", baseInitrdPath, err)
		}
		baseCPIO, err := decompressInitrd(baseData)
		if err != nil {
			os.Remove(tmp.Name())
			return "", fmt.Errorf("decompressing base initrd: %w", err)
		}
		baseCPIO = stripCPIOTrailer(baseCPIO)
		if _, err := gz.Write(baseCPIO); err != nil {
			os.Remove(tmp.Name())
			return "", err
		}
	}

	// Write our init entries LAST — Linux initramfs uses unlink+create
	// so last entry wins, overriding any /init from the base initrd.
	var initCPIO bytes.Buffer
	writeCPIODir(&initCPIO, "dev", 0xFFF0)
	writeCPIOCharDev(&initCPIO, "dev/console", 020620, 5, 1, 0xFFF1)

	if len(preinitBinary) > 0 {
		// On KVM: use the preinit stub as /init which mounts /proc, /dev,
		// sets up console fds, then execve's /sandal-init.
		// Go's runtime needs /proc and valid fds to initialize properly.
		writeCPIOFile(&initCPIO, "init", preinitBinary, 0100755, 0xFFF2)
		writeCPIOFile(&initCPIO, "sandal-init", binData, 0100755, 0xFFF3)
	} else {
		// On macOS VZ: the hypervisor handles /dev setup, so Go binary
		// can be /init directly.
		writeCPIOFile(&initCPIO, "init", binData, 0100755, 0xFFF2)
	}
	writeCPIOFile(&initCPIO, "TRAILER!!!", nil, 0, 0)
	if _, err := gz.Write(initCPIO.Bytes()); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}

	if err := gz.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("finalizing gzip: %w", err)
	}

	return tmp.Name(), nil
}

// CreateContainerOverlay is like CreateOverlay but replaces all exec switch_root
// calls with the given execCommand. This allows running a program (e.g. sandal)
// directly in the initramfs environment without requiring a root filesystem on disk.
func CreateContainerOverlay(initrdPath string, mounts []MountInfo, execCommand string) (string, error) {
	if len(mounts) == 0 && execCommand == "" {
		return initrdPath, nil
	}

	initScript, err := extractInit(initrdPath)
	if err != nil {
		return "", fmt.Errorf("extract init from initramfs: %w", err)
	}

	modified := string(initScript)
	if len(mounts) > 0 {
		modified = injectMounts(modified, mounts)
	}
	if execCommand != "" {
		modified = replaceSwitch(modified, execCommand)
	}

	overlay := newcCPIO("init", []byte(modified), 0100755)

	origData, err := os.ReadFile(initrdPath)
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp("", "sandal-initrd-*.img")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	if _, err := tmp.Write(origData); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if _, err := tmp.Write(overlay); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}

	return tmp.Name(), nil
}

// extractInit reads the initramfs (gzip-compressed or plain CPIO) and
// returns the contents of the /init file.
func extractInit(initrdPath string) ([]byte, error) {
	data, err := os.ReadFile(initrdPath)
	if err != nil {
		return nil, err
	}

	cpioData, err := decompressInitrd(data)
	if err != nil {
		return nil, err
	}

	return findInCPIO(cpioData, "init")
}

// decompressInitrd decompresses a gzip-compressed initrd to get raw CPIO data.
// If the data is not gzip-compressed, it is returned as-is.
func decompressInitrd(data []byte) ([]byte, error) {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data, nil
	}
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// injectMounts inserts virtiofs mount commands:
// 1. Early in the script (for initramfs-only boots with custom init=)
// 2. Before each exec switch_root (for normal Alpine boots)
func injectMounts(initScript string, mounts []MountInfo) string {
	// Mount in $sysroot for switch_root case
	var sysrootMounts strings.Builder
	sysrootMounts.WriteString("\n# sandal-vm: auto-mount virtiofs shares (sysroot)\n")
	sysrootMounts.WriteString("echo 'Loading virtiofs modules...'\n")
	sysrootMounts.WriteString("$MOCK modprobe fuse && echo 'fuse loaded' || echo 'fuse failed'\n")
	sysrootMounts.WriteString("$MOCK modprobe virtiofs && echo 'virtiofs loaded' || echo 'virtiofs failed'\n")
	for _, m := range mounts {
		opts := "rw"
		if m.ReadOnly {
			opts = "ro"
		}
		sysrootMounts.WriteString(fmt.Sprintf("$MOCK mkdir -p \"$sysroot\"/mnt/%s\n", m.Tag))
		sysrootMounts.WriteString(fmt.Sprintf("echo 'Mounting %s...'\n", m.Tag))
		sysrootMounts.WriteString(fmt.Sprintf("$MOCK mount -t virtiofs -o %s %s \"$sysroot\"/mnt/%s && echo 'Mounted %s' || echo 'Failed to mount %s'\n", opts, m.Tag, m.Tag, m.Tag, m.Tag))
	}

	// Mount directly in /mnt for initramfs-only case (when init=/bin/sh)
	var initramfsMounts strings.Builder
	initramfsMounts.WriteString("\n# sandal-vm: auto-mount virtiofs shares (initramfs)\n")
	initramfsMounts.WriteString("modprobe fuse 2>/dev/null || true\n")
	initramfsMounts.WriteString("modprobe virtiofs 2>/dev/null || true\n")
	for _, m := range mounts {
		opts := "rw"
		if m.ReadOnly {
			opts = "ro"
		}
		initramfsMounts.WriteString(fmt.Sprintf("mkdir -p /mnt/%s\n", m.Tag))
		initramfsMounts.WriteString(fmt.Sprintf("mount -t virtiofs -o %s %s /mnt/%s 2>/dev/null || true\n", opts, m.Tag, m.Tag))
	}

	lines := strings.Split(initScript, "\n")
	var result []string
	injectedEarly := false

	for _, line := range lines {
		if !injectedEarly && strings.Contains(line, "mount -t proc") {
			result = append(result, line)
			result = append(result, initramfsMounts.String())
			injectedEarly = true
			continue
		}
		if strings.Contains(line, "exec switch_root") {
			result = append(result, sysrootMounts.String())
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

// replaceSwitch replaces all exec switch_root lines with the given command.
func replaceSwitch(initScript string, command string) string {
	lines := strings.Split(initScript, "\n")
	var result []string
	for _, line := range lines {
		if strings.Contains(line, "exec switch_root") {
			result = append(result, "# sandal-vm: replaced switch_root with container exec")
			result = append(result, "exec "+command)
		} else {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

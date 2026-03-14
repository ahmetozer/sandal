package initrd

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"
)

// MountInfo describes a virtiofs mount to inject into the init script.
type MountInfo struct {
	Tag      string
	ReadOnly bool
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

	// Read and decompress the original initramfs to extract /init
	initScript, err := extractInit(initrdPath)
	if err != nil {
		return "", fmt.Errorf("extract init from initramfs: %w", err)
	}

	// Inject mount commands before switch_root
	modified := injectMounts(string(initScript), mounts)

	// Build CPIO overlay with modified init
	overlay := newcCPIO("init", []byte(modified), 0100755)

	// Create combined initramfs: original + overlay
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

	// Decompress if gzipped
	var cpioData []byte
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		r, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		cpioData, err = io.ReadAll(r)
		r.Close()
		if err != nil {
			return nil, fmt.Errorf("gzip read: %w", err)
		}
	} else {
		cpioData = data
	}

	return findInCPIO(cpioData, "init")
}

// findInCPIO parses a newc-format CPIO archive and returns the data for the
// named file.
func findInCPIO(data []byte, target string) ([]byte, error) {
	offset := 0
	for offset < len(data) {
		if offset+110 > len(data) {
			break
		}

		magic := string(data[offset : offset+6])
		if magic != "070701" && magic != "070702" {
			return nil, fmt.Errorf("invalid cpio magic at offset %d: %q", offset, magic)
		}

		filesize := parseHex(data[offset+54 : offset+62])
		namesize := parseHex(data[offset+94 : offset+102])

		nameEnd := offset + 110 + int(namesize)
		if nameEnd > len(data) {
			break
		}
		name := string(data[offset+110 : nameEnd-1]) // exclude null terminator

		// Data starts after header+name, aligned to 4 bytes
		dataStart := align4(offset + 110 + int(namesize))
		dataEnd := dataStart + int(filesize)

		if name == "TRAILER!!!" {
			break
		}

		// Strip leading "./" or "/" for comparison
		cleanName := strings.TrimPrefix(strings.TrimPrefix(name, "./"), "/")
		if cleanName == target && filesize > 0 {
			if dataEnd > len(data) {
				return nil, fmt.Errorf("cpio data overflow for %s", target)
			}
			return data[dataStart:dataEnd], nil
		}

		// Next entry: align data end to 4 bytes
		offset = align4(dataEnd)
	}

	return nil, fmt.Errorf("file %q not found in cpio archive", target)
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
		// Inject early, after /proc is mounted (needed for modprobe)
		if !injectedEarly && strings.Contains(line, "mount -t proc") {
			result = append(result, line)
			result = append(result, initramfsMounts.String())
			injectedEarly = true
			continue
		}
		// Inject before switch_root for normal boots
		if strings.Contains(line, "exec switch_root") {
			result = append(result, sysrootMounts.String())
		}
		result = append(result, line)
	}
	return strings.Join(result, "\n")
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
		// Strip the TRAILER entry so we can append more entries
		baseCPIO = stripCPIOTrailer(baseCPIO)
		if _, err := gz.Write(baseCPIO); err != nil {
			os.Remove(tmp.Name())
			return "", err
		}
	}

	// Append /init entry + final TRAILER
	cpioData := newcCPIO("init", binData, 0100755)
	if _, err := gz.Write(cpioData); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}

	if err := gz.Close(); err != nil {
		os.Remove(tmp.Name())
		return "", fmt.Errorf("finalizing gzip: %w", err)
	}

	return tmp.Name(), nil
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

// stripCPIOTrailer removes the TRAILER!!! entry and any trailing padding
// from a raw CPIO archive, allowing more entries to be appended.
func stripCPIOTrailer(data []byte) []byte {
	trailerHeader := []byte("070701")
	trailerName := "TRAILER!!!"

	// Scan from the end looking for the TRAILER entry
	// TRAILER entry = 110-byte header + "TRAILER!!!\0" (11 bytes) + padding
	for offset := len(data) - 1; offset >= 110; offset-- {
		if !bytes.Equal(data[offset-5:offset+1], trailerHeader) {
			continue
		}
		pos := offset - 5
		// Check if this header's name is TRAILER!!!
		nameSize := parseHex(data[pos+94 : pos+102])
		if nameSize < 11 {
			continue
		}
		nameStart := pos + 110
		nameEnd := nameStart + int(nameSize) - 1 // exclude null
		if nameEnd > len(data) {
			continue
		}
		if string(data[nameStart:nameEnd]) == trailerName {
			return data[:pos]
		}
	}
	return data
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

// newcCPIO builds a minimal newc-format CPIO archive containing a single file
// and a trailer.
func newcCPIO(name string, data []byte, mode uint32) []byte {
	var buf bytes.Buffer
	writeCPIOEntry(&buf, name, data, mode, 1)
	writeCPIOEntry(&buf, "TRAILER!!!", nil, 0, 1)
	return buf.Bytes()
}

func writeCPIOEntry(buf *bytes.Buffer, name string, data []byte, mode uint32, nlink uint32) {
	nameWithNull := name + "\x00"
	filesize := 0
	if data != nil {
		filesize = len(data)
	}

	// Header (6 magic + 13 fields * 8 hex chars = 110 bytes)
	fmt.Fprintf(buf, "070701")
	fmt.Fprintf(buf, "%08X", 1)        // ino
	fmt.Fprintf(buf, "%08X", mode)     // mode
	fmt.Fprintf(buf, "%08X", 0)        // uid
	fmt.Fprintf(buf, "%08X", 0)        // gid
	fmt.Fprintf(buf, "%08X", nlink)    // nlink
	fmt.Fprintf(buf, "%08X", 0)        // mtime
	fmt.Fprintf(buf, "%08X", filesize) // filesize
	fmt.Fprintf(buf, "%08X", 0)        // devmajor
	fmt.Fprintf(buf, "%08X", 0)        // devminor
	fmt.Fprintf(buf, "%08X", 0)        // rdevmajor
	fmt.Fprintf(buf, "%08X", 0)        // rdevminor
	fmt.Fprintf(buf, "%08X", len(nameWithNull))
	fmt.Fprintf(buf, "%08X", 0) // check

	buf.WriteString(nameWithNull)
	padTo4(buf)

	if filesize > 0 {
		buf.Write(data)
		padTo4(buf)
	}
}

func parseHex(b []byte) uint64 {
	var val uint64
	for _, c := range b {
		val <<= 4
		switch {
		case c >= '0' && c <= '9':
			val |= uint64(c - '0')
		case c >= 'a' && c <= 'f':
			val |= uint64(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			val |= uint64(c - 'A' + 10)
		}
	}
	return val
}

func align4(n int) int {
	return (n + 3) &^ 3
}

func padTo4(buf *bytes.Buffer) {
	for buf.Len()%4 != 0 {
		buf.WriteByte(0)
	}
}

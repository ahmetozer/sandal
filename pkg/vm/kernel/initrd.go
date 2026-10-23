package kernel

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
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
// Returns the path to a temporary file (caller must remove it), or
// initrdPath unchanged when there is nothing to inject: no mounts, or an
// /init that is not a shell script. The sandal initrd's /init is the sandal
// binary itself, which mounts virtiofs shares from SANDAL_VM_MOUNTS; editing
// it as text would corrupt the ELF and make PID 1 segfault.
func CreateOverlay(initrdPath string, mounts []MountInfo) (string, error) {
	if len(mounts) == 0 {
		return initrdPath, nil
	}

	initScript, err := extractInit(initrdPath)
	if err != nil {
		return "", fmt.Errorf("extract init from initramfs: %w", err)
	}
	if !isScript(initScript) {
		return initrdPath, nil
	}

	modified := injectMounts(string(initScript), mounts)
	overlay := newcCPIO("init", []byte(modified), 0100755)

	return appendOverlay(initrdPath, overlay)
}

// CreateFromBinary creates an initramfs CPIO archive with the given binary
// bytes as /init. If baseInitrdPath is non-empty, the base initrd's CPIO
// entries are merged into a single CPIO archive so kernel modules remain
// available. The binary is added as /init after the base entries.
//
// The result is a single gzip-compressed CPIO archive (not concatenated archives)
// because the kernel's initramfs unpacker stops processing at the first TRAILER
// entry within a CPIO stream.
//
// The returned path is content-addressed: identical (binary, base initrd) pairs
// always resolve to the same file under cacheDir() and are reused across runs.
// On a cache hit no archive is built and no I/O happens beyond hashing the
// inputs. The returned path is owned by the cache and MUST NOT be removed by
// the caller.
func CreateFromBinary(binData []byte, baseInitrdPath string) (string, error) {
	key, err := initrdCacheKey(binData, baseInitrdPath)
	if err != nil {
		return "", err
	}
	cDir := cacheDir()
	cachePath := filepath.Join(cDir, "initramfs-sandal-"+key+".img")
	if st, err := os.Stat(cachePath); err == nil && st.Size() > 0 {
		return cachePath, nil
	}

	if err := os.MkdirAll(cDir, 0755); err != nil {
		return "", fmt.Errorf("creating kernel cache dir: %w", err)
	}

	// Build into a temp file in the SAME directory as the final cache path so
	// the rename below is atomic. Concurrent builders racing on the same key
	// produce identical content; whichever rename runs last wins harmlessly.
	tmp, err := os.CreateTemp(cDir, ".initramfs-sandal-*.img.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpPath)
	}

	gz := gzip.NewWriter(tmp)

	// Include base initrd content (decompressed CPIO, trailer stripped)
	// for kernel modules.
	if baseInitrdPath != "" {
		baseData, err := os.ReadFile(baseInitrdPath)
		if err != nil {
			cleanup()
			return "", fmt.Errorf("reading base initrd %s: %w", baseInitrdPath, err)
		}
		baseCPIO, err := decompressInitrd(baseData)
		if err != nil {
			cleanup()
			return "", fmt.Errorf("decompressing base initrd: %w", err)
		}
		baseCPIO = stripCPIOTrailer(baseCPIO)
		if _, err := gz.Write(baseCPIO); err != nil {
			cleanup()
			return "", err
		}
	}

	// Write our init entries LAST — Linux initramfs uses unlink+create
	// so last entry wins, overriding any /init from the base initrd.
	var initCPIO bytes.Buffer
	writeCPIODir(&initCPIO, "dev", 0xFFF0)
	writeCPIOCharDev(&initCPIO, "dev/console", 020620, 5, 1, 0xFFF1)

	// Go's net package probes /etc/nsswitch.conf at init time to decide
	// whether to use cgo or pure-Go resolver. Without this file, the
	// probe hangs at PID 1 in initramfs. Provide minimal /etc files so
	// Go's runtime can initialize.
	writeCPIODir(&initCPIO, "etc", 0xFFE0)
	writeCPIOFile(&initCPIO, "etc/nsswitch.conf", []byte("hosts: files dns\n"), 0100644, 0xFFE1)
	writeCPIOFile(&initCPIO, "etc/resolv.conf", []byte(""), 0100644, 0xFFE2)
	writeCPIOFile(&initCPIO, "etc/hosts", []byte("127.0.0.1 localhost\n::1 localhost\n"), 0100644, 0xFFE3)

	// The Go binary runs directly as /init. The kernel opens /dev/console
	// (provided as a cpio char device above) for fds 0/1/2 when init is
	// exec'd, and importKernelCmdlineEnv() mounts devtmpfs and procfs
	// before any code that depends on them.
	writeCPIOFile(&initCPIO, "init", binData, 0100755, 0xFFF2)
	writeCPIOFile(&initCPIO, "TRAILER!!!", nil, 0, 0)
	if _, err := gz.Write(initCPIO.Bytes()); err != nil {
		cleanup()
		return "", err
	}

	if err := gz.Close(); err != nil {
		cleanup()
		return "", fmt.Errorf("finalizing gzip: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", fmt.Errorf("syncing initrd: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("closing initrd: %w", err)
	}
	if err := os.Rename(tmpPath, cachePath); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("publishing initrd cache entry: %w", err)
	}
	return cachePath, nil
}

// initrdCacheKey computes a content-address cache key from the sandal binary
// bytes and (optional) base initrd. Returns the first 16 bytes of sha256 as a
// hex string — 64 bits of entropy, plenty to avoid collisions across the
// handful of entries a single host accumulates.
func initrdCacheKey(binData []byte, baseInitrdPath string) (string, error) {
	h := sha256.New()
	h.Write(binData)
	// Length-prefix separator so concat(a,b) can never collide with concat(a',b').
	h.Write([]byte{0})
	if baseInitrdPath != "" {
		if err := hashFileInto(h, baseInitrdPath); err != nil {
			return "", fmt.Errorf("hashing base initrd %s: %w", baseInitrdPath, err)
		}
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:16]), nil
}

func hashFileInto(h io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(h, f)
	return err
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
	if !isScript(initScript) {
		return "", fmt.Errorf("initramfs /init is not a shell script")
	}

	modified := string(initScript)
	if len(mounts) > 0 {
		modified = injectMounts(modified, mounts)
	}
	if execCommand != "" {
		modified = replaceSwitch(modified, execCommand)
	}

	overlay := newcCPIO("init", []byte(modified), 0100755)

	return appendOverlay(initrdPath, overlay)
}

// appendOverlay writes a temp copy of initrdPath followed by the overlay
// archive. The kernel only detects an uncompressed archive that starts on a
// 4-byte boundary of the whole image (init/initramfs.c tracks the offset in
// this_header); otherwise it fails with "invalid magic at start of compressed
// archive" and silently ignores the overlay. NUL padding is skipped, so pad.
func appendOverlay(initrdPath string, overlay []byte) (string, error) {
	origData, err := os.ReadFile(initrdPath)
	if err != nil {
		return "", err
	}

	tmp, err := os.CreateTemp("", "sandal-initrd-*.img")
	if err != nil {
		return "", err
	}
	defer tmp.Close()

	pad := make([]byte, (4-len(origData)%4)%4)
	for _, b := range [][]byte{origData, pad, overlay} {
		if _, err := tmp.Write(b); err != nil {
			os.Remove(tmp.Name())
			return "", err
		}
	}
	return tmp.Name(), nil
}

// isScript reports whether an /init payload is an interpreter script
// (starts with "#!") and so can be edited line by line.
func isScript(init []byte) bool {
	return bytes.HasPrefix(init, []byte("#!"))
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
// If the data is not gzip-compressed, it is returned as-is. Leading gzip
// members are decompressed one by one; anything after them that is not gzip
// (e.g. an uncompressed overlay archive appended by CreateOverlay) is kept
// as-is, mirroring how the kernel accepts mixed concatenated initramfs.
func decompressInitrd(data []byte) ([]byte, error) {
	var out bytes.Buffer
	rest := data
	for {
		for len(rest) > 0 && rest[0] == 0 {
			rest = rest[1:]
		}
		if len(rest) < 2 || rest[0] != 0x1f || rest[1] != 0x8b {
			break
		}
		// bytes.Reader is an io.ByteReader, so the decompressor stops exactly
		// at the end of the member and br.Len() is the unread remainder.
		br := bytes.NewReader(rest)
		r, err := gzip.NewReader(br)
		if err != nil {
			return nil, err
		}
		r.Multistream(false)
		if _, err := io.Copy(&out, r); err != nil {
			return nil, err
		}
		r.Close()
		rest = rest[len(rest)-br.Len():]
	}
	if out.Len() == 0 {
		return data, nil
	}
	out.Write(rest)
	return out.Bytes(), nil
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

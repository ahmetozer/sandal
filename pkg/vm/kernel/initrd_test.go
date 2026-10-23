package kernel

import (
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/ahmetozer/sandal/pkg/env"
)

// fakeSandalBinary mimics the sandal ELF that becomes /init: a non-script
// blob that happens to contain the very literals injectMounts searches for,
// because they live in injectMounts' own source and so in .rodata.
func fakeSandalBinary() []byte {
	var b bytes.Buffer
	b.Write([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
	b.Write(bytes.Repeat([]byte{0}, 64))
	b.WriteString("....mount -t proc\n....")
	b.Write([]byte{0x00, 0xff, 0x10, 0x20})
	b.WriteString("exec switch_root\n")
	b.Write(bytes.Repeat([]byte{0xAB}, 64))
	return b.Bytes()
}

const alpineLikeInit = `#!/bin/sh
sysroot=/sysroot
mount -t proc -o noexec,nosuid,nodev proc /proc
echo booting
exec switch_root $sysroot /sbin/init
`

func useTempKernelDir(t *testing.T) {
	t.Helper()
	old := env.BaseKernelDir
	env.BaseKernelDir = t.TempDir()
	t.Setenv("TMPDIR", t.TempDir()) // keep any overlay temp files inside the test
	t.Cleanup(func() { env.BaseKernelDir = old })
}

// writeGzipCPIO writes a gzip-compressed newc archive with the given files.
func writeGzipCPIO(t *testing.T, path string, files ...[2]string) {
	t.Helper()
	var c bytes.Buffer
	for i, f := range files {
		writeCPIOFile(&c, f[0], []byte(f[1]), 0100755, uint32(i+1))
	}
	writeCPIOFile(&c, "TRAILER!!!", nil, 0, 0)
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write(c.Bytes())
	w.Close()
	if err := os.WriteFile(path, gz.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
}

func testMounts() []MountInfo {
	return []MountInfo{{Tag: "fs-0"}, {Tag: "host-etc", ReadOnly: true}}
}

// The sandal initrd's /init is the sandal binary. CreateOverlay must not
// treat it as a shell script and append a corrupted copy as /init.
func TestCreateOverlaySkipsBinaryInit(t *testing.T) {
	useTempKernelDir(t)
	bin := fakeSandalBinary()
	initrd, err := CreateFromBinary(bin, "")
	if err != nil {
		t.Fatal(err)
	}

	out, err := CreateOverlay(initrd, testMounts())
	if err != nil {
		t.Fatal(err)
	}
	if out != initrd {
		defer os.Remove(out)
	}

	got, err := extractInit(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bin) {
		t.Fatalf("effective /init was modified: len %d, want %d (binary must be byte-identical)", len(got), len(bin))
	}
	if out != initrd {
		t.Errorf("CreateOverlay returned a new initrd %s for a binary /init; want the original path", out)
	}
}

// Same as above, but with a base initrd whose own /init is a script (e.g. an
// Alpine initramfs-virt next to the kernel). The merged archive holds two
// "init" entries; the kernel keeps the last one (the sandal binary).
func TestCreateOverlaySkipsBinaryInitOverScriptBase(t *testing.T) {
	useTempKernelDir(t)
	base := filepath.Join(t.TempDir(), "initramfs-virt")
	writeGzipCPIO(t, base, [2]string{"init", alpineLikeInit})

	bin := fakeSandalBinary()
	initrd, err := CreateFromBinary(bin, base)
	if err != nil {
		t.Fatal(err)
	}

	out, err := CreateOverlay(initrd, testMounts())
	if err != nil {
		t.Fatal(err)
	}
	if out != initrd {
		defer os.Remove(out)
		t.Errorf("CreateOverlay returned a new initrd %s; want the original path", out)
	}
	got, err := extractInit(out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, bin) {
		t.Fatalf("effective /init is not the sandal binary (len %d, want %d)", len(got), len(bin))
	}
}

// A stock initramfs whose /init is a shell script still gets the virtiofs
// mount commands injected.
func TestCreateOverlayInjectsIntoScriptInit(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	initrd := filepath.Join(t.TempDir(), "initramfs-virt")
	writeGzipCPIO(t, initrd, [2]string{"init", alpineLikeInit})

	out, err := CreateOverlay(initrd, testMounts())
	if err != nil {
		t.Fatal(err)
	}
	if out == initrd {
		t.Fatal("CreateOverlay returned the original path for a script /init; want an overlay")
	}
	defer os.Remove(out)

	got, err := extractInit(out)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	for _, want := range []string{
		"#!/bin/sh\n",
		"mount -t virtiofs -o rw fs-0 /mnt/fs-0",
		"mount -t virtiofs -o ro host-etc \"$sysroot\"/mnt/host-etc",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("overlay /init missing %q:\n%s", want, s)
		}
	}
	if strings.Index(s, "fs-0 \"$sysroot\"") > strings.Index(s, "exec switch_root") {
		t.Error("sysroot mounts not placed before exec switch_root")
	}
}

// findInCPIO must return the last entry with the name, as the kernel does,
// including across concatenated archives.
func TestFindInCPIOReturnsLastMatch(t *testing.T) {
	var c bytes.Buffer
	writeCPIOFile(&c, "init", []byte("first"), 0100755, 1)
	writeCPIOFile(&c, "other", []byte("x"), 0100644, 2)
	writeCPIOFile(&c, "init", []byte("second"), 0100755, 3)
	writeCPIOFile(&c, "TRAILER!!!", nil, 0, 0)
	c.Write(bytes.Repeat([]byte{0}, 512)) // padding between archives
	c.Write(newcCPIO("init", []byte("third"), 0100755))

	got, err := findInCPIO(c.Bytes(), "init")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "third" {
		t.Fatalf("findInCPIO = %q, want %q", got, "third")
	}

	if _, err := findInCPIO(c.Bytes(), "missing"); err == nil {
		t.Error("expected error for missing entry")
	}
}

// The kernel only recognises an uncompressed archive appended after a
// compressed one when it starts on a 4-byte boundary of the whole initrd;
// otherwise it logs "invalid magic at start of compressed archive" and drops
// the overlay. The overlay must be padded into alignment.
func TestCreateOverlayAlignsAppendedArchive(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	dir := t.TempDir()
	var initrd string
	var origLen int64
	for i := 0; ; i++ {
		initrd = filepath.Join(dir, "initramfs-virt")
		writeGzipCPIO(t, initrd, [2]string{"init", alpineLikeInit}, [2]string{"pad", strings.Repeat("x", i)})
		st, err := os.Stat(initrd)
		if err != nil {
			t.Fatal(err)
		}
		if st.Size()%4 != 0 {
			origLen = st.Size()
			break
		}
	}

	out, err := CreateOverlay(initrd, testMounts())
	if err != nil {
		t.Fatal(err)
	}
	if out == initrd {
		t.Fatal("expected an overlay")
	}
	defer os.Remove(out)

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	off := origLen
	for off < int64(len(data)) && data[off] == 0 {
		off++
	}
	if !bytes.HasPrefix(data[off:], []byte("070701")) {
		t.Fatalf("no cpio header after original initrd (offset %d)", off)
	}
	if off%4 != 0 {
		t.Fatalf("overlay archive starts at offset %d (len(orig)=%d), not 4-byte aligned", off, origLen)
	}
}

// The virtio-fs tag is announced by the driver a moment after modprobe
// returns, so a mount issued right away fails with "tag not found". The
// first injected mount of each block must be retried until the tags show up.
func TestInjectMountsRetriesUntilTagAppears(t *testing.T) {
	s := injectMounts(alpineLikeInit, testMounts())
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if !strings.Contains(line, "mount -t virtiofs") {
			continue
		}
		first := strings.Contains(line, testMounts()[0].Tag)
		n++
		if retried := strings.Contains(line, "sleep") && strings.Contains(line, "break"); retried != first {
			t.Errorf("virtiofs mount retried = %v, want %v (only the first mount waits): %q", retried, first, line)
		}
	}
	if n != 2*len(testMounts()) {
		t.Errorf("found %d virtiofs mount lines, want %d", n, 2*len(testMounts()))
	}
}

// A stock initramfs ships virtio_mmio as a module that only its coldplug
// step loads, which runs long after the early mount block. Without loading
// it first the virtio-fs device does not exist yet and the mount cannot work.
func TestInjectMountsLoadsVirtioMMIOBeforeEarlyMount(t *testing.T) {
	s := injectMounts(alpineLikeInit, testMounts())
	mod := strings.Index(s, "modprobe virtio_mmio")
	mnt := strings.Index(s, "mount -t virtiofs")
	if mod < 0 || mod > mnt {
		t.Errorf("modprobe virtio_mmio must precede the first virtiofs mount (mod=%d mount=%d):\n%s", mod, mnt, s)
	}
}

// runInjected runs the injected script with sh, with mount/sleep/mkdir/
// modprobe faked: virtiofs tags appear after appear seconds of sleep. It
// returns the number of sleeps and the virtiofs mount attempts per tag.
func runInjected(t *testing.T, appear int) (sleeps int, tries map[string]int) {
	t.Helper()
	var script strings.Builder
	script.WriteString(`sleeps=0
mkdir() { :; }
modprobe() { :; }
sleep() { sleeps=$((sleeps+1)); }
mount() {
	[ "$2" = virtiofs ] || return 0
	echo "try $5"
	[ "$sleeps" -ge ` + strconv.Itoa(appear) + ` ]
}
`)
	for _, line := range strings.Split(injectMounts(alpineLikeInit, testMounts()), "\n") {
		if !strings.Contains(line, "exec switch_root") {
			script.WriteString(line + "\n")
		}
	}
	script.WriteString("echo \"sleeps $sleeps\"\n")
	out, err := exec.Command("sh", "-c", script.String()).CombinedOutput()
	if err != nil {
		t.Fatalf("sh: %v\n%s", err, out)
	}
	tries = map[string]int{}
	for _, l := range strings.Split(string(out), "\n") {
		if tag, ok := strings.CutPrefix(l, "try "); ok {
			tries[tag]++
		}
		if n, ok := strings.CutPrefix(l, "sleeps "); ok {
			sleeps, _ = strconv.Atoi(n)
		}
	}
	return sleeps, tries
}

// The virtio-fs device announces all its tags together, so each injected
// block waits (up to 5 s) only for its first mount and tries the others
// once: a missing tag must not cost 5 s per mount.
func TestInjectMountsSharedWait(t *testing.T) {
	sleeps, tries := runInjected(t, 1000) // tags never appear
	if sleeps > 10 {
		t.Errorf("slept %d s over both blocks with no tags, want at most 5 s per block", sleeps)
	}
	if tries["host-etc"] != 2 {
		t.Errorf("host-etc tried %d times, want once per block", tries["host-etc"])
	}

	sleeps, tries = runInjected(t, 2) // tags appear after 2 s
	if sleeps != 2 {
		t.Errorf("slept %d s, want 2 (until the tags appear)", sleeps)
	}
	if tries["fs-0"] != 4 || tries["host-etc"] != 2 {
		t.Errorf("tries = %v, want fs-0 3 then 1, host-etc once per block", tries)
	}
}

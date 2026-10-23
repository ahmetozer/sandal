package kernel

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
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
